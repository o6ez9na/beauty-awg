#!/usr/bin/env python3
"""Merge every scanner's raw output into one self-contained HTML report.

Reads the artifacts downloaded by the `security-report` job and emits a single
styled, dependency-free HTML file (plus a short Markdown digest for the run's
Step Summary). Robust to missing files: a scanner that didn't run just shows an
empty/greyed-out section.

Inputs (CLI):
  --artifacts DIR   root dir holding the downloaded artifact subfolders
  --out FILE        HTML output path
  --summary FILE    Markdown digest output path (GitHub Step Summary)
  --status "k=v,.." per-job results, e.g. "osv-scanner=failure,gitleaks=success"
  --repo, --sha, --ref, --run-url   metadata for the header (optional)
"""
from __future__ import annotations

import argparse
import glob
import html
import json
import os
from datetime import datetime, timezone

SEV_ORDER = {"critical": 4, "high": 3, "medium": 2, "low": 1, "info": 0, "unknown": 0}
SEV_COLORS = {
    "critical": "#b3123b",
    "high": "#d9480f",
    "medium": "#b8860b",
    "low": "#2f6f4f",
    "info": "#4a5568",
    "unknown": "#4a5568",
}


def sev_from_cvss(score: float) -> str:
    if score >= 9.0:
        return "critical"
    if score >= 7.0:
        return "high"
    if score >= 4.0:
        return "medium"
    if score > 0:
        return "low"
    return "unknown"


def sev_from_sarif_level(level: str) -> str:
    return {"error": "high", "warning": "medium", "note": "low", "none": "info"}.get(
        (level or "").lower(), "info"
    )


def norm_npm_sev(s: str) -> str:
    s = (s or "").lower()
    return {"moderate": "medium"}.get(s, s) or "unknown"


def find_one(root: str, *patterns: str) -> str | None:
    for pat in patterns:
        hits = sorted(glob.glob(os.path.join(root, "**", pat), recursive=True))
        if hits:
            return hits[0]
    return None


def load_json(path: str | None):
    if not path or not os.path.exists(path):
        return None
    try:
        with open(path, encoding="utf-8") as fh:
            return json.load(fh)
    except (json.JSONDecodeError, OSError):
        return None


# --------------------------------------------------------------------------- #
# Parsers -> list of normalized findings: dict(tool, id, url, severity,
#            location, title). Also return raw text where useful.
# --------------------------------------------------------------------------- #
def parse_osv(root: str) -> list[dict]:
    data = load_json(find_one(root, "osv-results.json"))
    out: list[dict] = []
    if not data:
        return out
    for res in data.get("results", []) or []:
        src = (res.get("source", {}) or {}).get("path", "")
        for pkg in res.get("packages", []) or []:
            p = pkg.get("package", {}) or {}
            name, ver, eco = p.get("name", "?"), p.get("version", "?"), p.get("ecosystem", "")
            # map vuln id -> details/fixed for enrichment
            details: dict[str, dict] = {}
            for v in pkg.get("vulnerabilities", []) or []:
                fixed = ""
                for aff in v.get("affected", []) or []:
                    for rng in aff.get("ranges", []) or []:
                        for ev in rng.get("events", []) or []:
                            if ev.get("fixed"):
                                fixed = ev["fixed"]
                details[v.get("id", "")] = {
                    "summary": v.get("summary") or (v.get("details", "")[:140]),
                    "fixed": fixed,
                }
            for grp in pkg.get("groups", []) or []:
                ids = grp.get("ids", []) or []
                sev = "unknown"
                ms = grp.get("max_severity", "")
                if ms not in (None, ""):
                    try:
                        sev = sev_from_cvss(float(ms))
                    except ValueError:
                        pass
                primary = ids[0] if ids else "?"
                d = details.get(primary, {})
                out.append(
                    {
                        "tool": "osv",
                        "id": ", ".join(ids),
                        "url": f"https://osv.dev/vulnerability/{primary}",
                        "severity": sev,
                        "location": f"{eco}: {name}@{ver}" + (f"  ({src})" if src else ""),
                        "title": d.get("summary", ""),
                        "fixed": d.get("fixed", ""),
                    }
                )
    return out


def parse_npm(root: str) -> tuple[list[dict], dict]:
    data = load_json(find_one(root, "npm-audit.json"))
    out: list[dict] = []
    if not data:
        return out, {}
    meta = (data.get("metadata", {}) or {}).get("vulnerabilities", {}) or {}
    for name, v in (data.get("vulnerabilities", {}) or {}).items():
        via = v.get("via", []) or []
        titles = [x if isinstance(x, str) else x.get("title", "") for x in via]
        fix = v.get("fixAvailable", False)
        fix_txt = "yes" if fix is True else ("no" if fix is False else "via major bump")
        out.append(
            {
                "tool": "npm",
                "id": name,
                "url": (via[0].get("url") if via and isinstance(via[0], dict) else "") or "",
                "severity": norm_npm_sev(v.get("severity")),
                "location": f"npm: {name}",
                "title": "; ".join(t for t in titles if t)[:160],
                "fixed": fix_txt,
            }
        )
    return out, meta


def parse_sarif(root: str, tool: str, filename: str, id_url=None) -> list[dict]:
    data = load_json(find_one(root, filename))
    out: list[dict] = []
    if not data:
        return out
    for run in data.get("runs", []) or []:
        rules = {}
        for r in ((run.get("tool", {}) or {}).get("driver", {}) or {}).get("rules", []) or []:
            rules[r.get("id", "")] = r
        for res in run.get("results", []) or []:
            rid = res.get("ruleId") or (res.get("rule", {}) or {}).get("id", "") or "?"
            rule = rules.get(rid, {})
            # severity: prefer explicit properties, fall back to SARIF level
            sev = None
            props = {**(rule.get("properties", {}) or {}), **(res.get("properties", {}) or {})}
            for key in ("security-severity", "severity"):
                if key in props and props[key] not in (None, ""):
                    val = props[key]
                    try:
                        sev = sev_from_cvss(float(val))
                    except (TypeError, ValueError):
                        sev = norm_npm_sev(str(val))
                    break
            if not sev:
                sev = sev_from_sarif_level(res.get("level", ""))
            loc = ""
            locs = res.get("locations", []) or []
            if locs:
                pl = (locs[0].get("physicalLocation", {}) or {})
                uri = (pl.get("artifactLocation", {}) or {}).get("uri", "")
                line = (pl.get("region", {}) or {}).get("startLine", "")
                loc = f"{uri}:{line}" if uri else ""
            out.append(
                {
                    "tool": tool,
                    "id": rid,
                    "url": id_url(rid) if id_url else "",
                    "severity": sev,
                    "location": loc,
                    "title": (res.get("message", {}) or {}).get("text", "")[:200],
                    "fixed": "",
                }
            )
    return out


def read_text(root: str, filename: str) -> str:
    p = find_one(root, filename)
    if not p:
        return ""
    try:
        with open(p, encoding="utf-8", errors="replace") as fh:
            return fh.read()
    except OSError:
        return ""


# --------------------------------------------------------------------------- #
# HTML rendering
# --------------------------------------------------------------------------- #
def badge(sev: str) -> str:
    sev = sev if sev in SEV_COLORS else "unknown"
    return f'<span class="badge" style="background:{SEV_COLORS[sev]}">{sev}</span>'


def link(text: str, url: str) -> str:
    text = html.escape(text or "")
    if url:
        return f'<a href="{html.escape(url)}" target="_blank" rel="noopener">{text}</a>'
    return text


def findings_table(findings: list[dict], cols: list[tuple[str, str]]) -> str:
    if not findings:
        return '<p class="empty">No findings.</p>'
    findings = sorted(findings, key=lambda f: -SEV_ORDER.get(f["severity"], 0))
    head = "".join(f"<th>{html.escape(h)}</th>" for _, h in cols)
    rows = []
    for f in findings:
        cells = []
        for key, _ in cols:
            if key == "severity":
                cells.append(f"<td>{badge(f['severity'])}</td>")
            elif key == "id":
                cells.append(f"<td class='mono'>{link(f['id'], f.get('url',''))}</td>")
            else:
                cells.append(f"<td>{html.escape(str(f.get(key, '')))}</td>")
        rows.append("<tr>" + "".join(cells) + "</tr>")
    return f"<table><thead><tr>{head}</tr></thead><tbody>{''.join(rows)}</tbody></table>"


def section(title: str, status: str, body: str, count: int) -> str:
    chip = f'<span class="status {status}">{status}</span>'
    cnt = f'<span class="count">{count} finding(s)</span>' if count else ""
    return (
        f'<details {"open" if count else ""}><summary>'
        f"<span class='sec-title'>{html.escape(title)}</span> {chip} {cnt}"
        f"</summary><div class='sec-body'>{body}</div></details>"
    )


def tile(label: str, value, color: str) -> str:
    return (
        f'<div class="tile" style="border-top:3px solid {color}">'
        f'<div class="tile-val">{value}</div><div class="tile-lbl">{html.escape(label)}</div></div>'
    )


CSS = """
:root{--bg:#f6f7f9;--card:#fff;--fg:#1a202c;--muted:#5a6472;--line:#e2e8f0}
@media(prefers-color-scheme:dark){:root{--bg:#0f1420;--card:#171d2b;--fg:#e6eaf0;--muted:#93a0b4;--line:#2a3446}}
*{box-sizing:border-box}
body{margin:0;font:15px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;background:var(--bg);color:var(--fg)}
.wrap{max-width:1080px;margin:0 auto;padding:32px 20px 80px}
h1{font-size:26px;margin:0 0 4px}
.meta{color:var(--muted);font-size:13px;margin-bottom:20px}
.meta code{background:var(--card);padding:1px 6px;border-radius:4px}
.verdict{display:inline-block;padding:6px 14px;border-radius:999px;font-weight:700;color:#fff;margin:8px 0 20px}
.tiles{display:grid;grid-template-columns:repeat(auto-fit,minmax(120px,1fr));gap:12px;margin-bottom:14px}
.tile{background:var(--card);border-radius:10px;padding:14px 16px;box-shadow:0 1px 2px rgba(0,0,0,.06)}
.tile-val{font-size:28px;font-weight:700}
.tile-lbl{color:var(--muted);font-size:12px;text-transform:uppercase;letter-spacing:.04em}
.chips{display:flex;flex-wrap:wrap;gap:8px;margin:6px 0 26px}
.chip{background:var(--card);border:1px solid var(--line);border-radius:999px;padding:5px 12px;font-size:13px;display:flex;gap:8px;align-items:center}
.dot{width:9px;height:9px;border-radius:50%}
details{background:var(--card);border:1px solid var(--line);border-radius:12px;margin:12px 0;overflow:hidden}
summary{cursor:pointer;padding:16px 18px;font-size:16px;display:flex;align-items:center;gap:12px;list-style:none}
summary::-webkit-details-marker{display:none}
.sec-title{font-weight:600;flex:1}
.count{color:var(--muted);font-size:13px}
.sec-body{padding:0 18px 18px;overflow-x:auto}
.status{font-size:12px;font-weight:700;text-transform:uppercase;padding:3px 9px;border-radius:6px}
.status.success,.status.pass{background:#1c7c4a;color:#fff}
.status.failure,.status.fail{background:#b3123b;color:#fff}
.status.skipped,.status.info,.status.neutral{background:#4a5568;color:#fff}
.badge{color:#fff;font-size:11px;font-weight:700;text-transform:uppercase;padding:2px 8px;border-radius:5px}
table{border-collapse:collapse;width:100%;font-size:13.5px;margin-top:6px}
th,td{text-align:left;padding:8px 10px;border-bottom:1px solid var(--line);vertical-align:top}
th{color:var(--muted);font-size:12px;text-transform:uppercase;letter-spacing:.03em}
.mono{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12.5px}
a{color:#3b82f6}
pre{background:var(--bg);border:1px solid var(--line);border-radius:8px;padding:12px;overflow-x:auto;font-size:12.5px;white-space:pre-wrap;word-break:break-word}
.empty{color:var(--muted);font-style:italic}
footer{color:var(--muted);font-size:12px;margin-top:30px;text-align:center}
"""


def build(args) -> None:
    root = args.artifacts
    status = {}
    for kv in (args.status or "").split(","):
        if "=" in kv:
            k, v = kv.split("=", 1)
            status[k.strip()] = v.strip()

    osv = parse_osv(root)
    npm, npm_meta = parse_npm(root)
    gvc = parse_sarif(root, "govulncheck", "govulncheck.sarif",
                      id_url=lambda i: f"https://pkg.go.dev/vuln/{i}" if i.startswith("GO-") else "")
    gvc_txt = read_text(root, "govulncheck.txt")
    semgrep = parse_sarif(root, "semgrep", "semgrep.sarif")
    gosec = parse_sarif(root, "gosec", "gosec.sarif")

    blocking = osv + npm + gvc  # SCA layer drives the verdict tiles
    all_findings = blocking + semgrep + gosec
    counts = {s: 0 for s in ("critical", "high", "medium", "low", "info", "unknown")}
    for f in all_findings:
        counts[f["severity"] if f["severity"] in counts else "unknown"] += 1

    blk_high = sum(1 for f in blocking if f["severity"] in ("critical", "high"))
    gl = status.get("gitleaks", "")
    overall_fail = (
        blk_high > 0
        or gl == "failure"
        or any(status.get(j) == "failure" for j in ("osv-scanner", "govulncheck", "npm-audit"))
    )
    verdict = ("BLOCKED — high/critical issues" if overall_fail
               else "PASSED — no blocking issues")
    verdict_color = "#b3123b" if overall_fail else "#1c7c4a"

    # tiles
    tiles = "".join([
        tile("Total findings", len(all_findings), "#4a5568"),
        tile("Critical", counts["critical"], SEV_COLORS["critical"]),
        tile("High", counts["high"], SEV_COLORS["high"]),
        tile("Medium", counts["medium"], SEV_COLORS["medium"]),
        tile("Low", counts["low"], SEV_COLORS["low"]),
    ])

    # tool status chips
    tool_status = {
        "OSV-Scanner": status.get("osv-scanner", "neutral"),
        "govulncheck": status.get("govulncheck", "neutral"),
        "npm audit": status.get("npm-audit", "neutral"),
        "Semgrep": status.get("semgrep", "neutral"),
        "gosec": status.get("gosec", "neutral"),
        "Gitleaks": status.get("gitleaks", "neutral"),
    }
    chips = "".join(
        f'<span class="chip"><span class="dot" style="background:'
        f'{"#1c7c4a" if v=="success" else "#b3123b" if v=="failure" else "#4a5568"}"></span>'
        f"{html.escape(k)} · {html.escape(v)}</span>"
        for k, v in tool_status.items()
    )

    # sections
    secs = []
    secs.append(section(
        "OSV-Scanner — dependency vulnerabilities (JS + Go)",
        status.get("osv-scanner", "neutral"),
        '<p class="empty">Blocks on CVSS ≥ 7.0. Low/medium are informational.</p>'
        + findings_table(osv, [("severity", "Severity"), ("id", "Advisory"),
                               ("location", "Package"), ("fixed", "Fixed in"),
                               ("title", "Summary")]),
        len(osv)))
    secs.append(section(
        "govulncheck — reachable Go vulnerabilities",
        status.get("govulncheck", "neutral"),
        findings_table(gvc, [("severity", "Severity"), ("id", "Advisory"),
                             ("location", "Location"), ("title", "Summary")])
        + (f"<h4>Full report (call traces + fix versions)</h4><pre>{html.escape(gvc_txt)}</pre>"
           if gvc_txt else ""),
        len(gvc)))
    npm_totals = ", ".join(f"{k}: {npm_meta.get(k, 0)}" for k in ("critical", "high", "moderate", "low")) if npm_meta else ""
    secs.append(section(
        "npm audit — JS/TS dependency vulnerabilities",
        status.get("npm-audit", "neutral"),
        (f'<p class="empty">Totals — {npm_totals}. Blocks on high+.</p>' if npm_totals else "")
        + findings_table(npm, [("severity", "Severity"), ("id", "Package"),
                               ("fixed", "Fix available"), ("title", "Advisories")]),
        len(npm)))
    secs.append(section(
        "Semgrep OSS — SAST (informational)",
        status.get("semgrep", "neutral"),
        findings_table(semgrep, [("severity", "Severity"), ("id", "Rule"),
                                 ("location", "Location"), ("title", "Message")]),
        len(semgrep)))
    secs.append(section(
        "gosec — Go SAST (informational)",
        status.get("gosec", "neutral"),
        findings_table(gosec, [("severity", "Severity"), ("id", "Rule"),
                               ("location", "Location"), ("title", "Message")]),
        len(gosec)))
    secs.append(section(
        "Gitleaks — secret scanning",
        gl or "neutral",
        f'<p>Whole-repo + history scan. Job result: <b>{html.escape(gl or "not run")}</b>. '
        f'Any leak fails the build; see the Gitleaks job log for matched paths.</p>',
        0))

    now = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
    meta_bits = []
    if args.repo:
        meta_bits.append(f"repo <code>{html.escape(args.repo)}</code>")
    if args.ref:
        meta_bits.append(f"ref <code>{html.escape(args.ref)}</code>")
    if args.sha:
        meta_bits.append(f"commit <code>{html.escape(args.sha[:10])}</code>")
    meta_bits.append(now)
    run_link = f' · <a href="{html.escape(args.run_url)}" target="_blank" rel="noopener">workflow run</a>' if args.run_url else ""

    doc = f"""<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Security report</title><style>{CSS}</style></head><body><div class="wrap">
<h1>🛡️ Security report</h1>
<div class="meta">{' · '.join(meta_bits)}{run_link}</div>
<div class="verdict" style="background:{verdict_color}">{verdict}</div>
<div class="tiles">{tiles}</div>
<div class="chips">{chips}</div>
{''.join(secs)}
<footer>Generated by security.yml · OSV-Scanner · govulncheck · npm audit · Semgrep OSS · gosec · Gitleaks</footer>
</div></body></html>"""

    # Emit pure ASCII: every non-ASCII char (emoji, em-dash, ·, ≥, smart quotes
    # from tool output) becomes a numeric HTML entity, so the report renders the
    # same regardless of the charset the viewer/artifact server assumes.
    ascii_doc = doc.encode("ascii", "xmlcharrefreplace").decode("ascii")
    with open(args.out, "w", encoding="utf-8") as fh:
        fh.write(ascii_doc)

    # Full Markdown report for the GitHub UI (the run's Summary tab).
    if args.summary:
        md = render_markdown(
            verdict, overall_fail, counts, all_findings, tool_status,
            osv, gvc, gvc_txt, npm, npm_meta, semgrep, gosec, gl, args,
        )
        with open(args.summary, "a", encoding="utf-8") as fh:
            fh.write(md)

    print(f"Wrote {args.out}  (total findings: {len(all_findings)}, verdict: {verdict})")


SEV_EMOJI = {"critical": "🔴", "high": "🟠", "medium": "🟡", "low": "🔵",
             "info": "⚪", "unknown": "⚪"}
RESULT_EMOJI = {"success": "✅", "failure": "❌", "skipped": "⏭️", "neutral": "➖"}


def md_escape(s: str) -> str:
    return (s or "").replace("|", "\\|").replace("\n", " ").strip()


def md_link(text: str, url: str) -> str:
    text = md_escape(text)
    return f"[{text}]({url})" if url else text


def md_table(findings: list[dict], cols: list[tuple[str, str]]) -> str:
    if not findings:
        return "_No findings._\n"
    findings = sorted(findings, key=lambda f: -SEV_ORDER.get(f["severity"], 0))
    header = "| " + " | ".join(h for _, h in cols) + " |"
    sep = "|" + "|".join("---" for _ in cols) + "|"
    rows = []
    for f in findings:
        cells = []
        for key, _ in cols:
            if key == "severity":
                s = f["severity"]
                cells.append(f"{SEV_EMOJI.get(s, '⚪')} {s}")
            elif key == "id":
                cells.append(md_link(f["id"], f.get("url", "")))
            else:
                cells.append(md_escape(str(f.get(key, ""))))
        rows.append("| " + " | ".join(cells) + " |")
    return "\n".join([header, sep, *rows]) + "\n"


def md_section(title: str, result: str, count: int, body: str) -> str:
    chip = RESULT_EMOJI.get(result, "➖")
    open_attr = " open" if count else ""
    label = f"{chip} {title} — {count} finding(s)"
    return f"<details{open_attr}><summary><b>{label}</b></summary>\n\n{body}\n</details>\n\n"


def render_markdown(verdict, overall_fail, counts, all_findings, tool_status,
                    osv, gvc, gvc_txt, npm, npm_meta, semgrep, gosec, gl, args) -> str:
    head = "❌" if overall_fail else "✅"
    out = [f"# {head} Security report", ""]
    if args.sha or args.ref:
        out.append(f"`{md_escape(args.ref)}` @ `{md_escape((args.sha or '')[:10])}`\n")
    out += [
        f"**{verdict}**", "",
        "| Total | 🔴 Critical | 🟠 High | 🟡 Medium | 🔵 Low |",
        "|--:|--:|--:|--:|--:|",
        f"| {len(all_findings)} | {counts['critical']} | {counts['high']} "
        f"| {counts['medium']} | {counts['low']} |", "",
        "| Tool | Result |", "|---|---|",
    ]
    out += [f"| {k} | {RESULT_EMOJI.get(v,'➖')} {v} |" for k, v in tool_status.items()]
    out += ["", "---", ""]

    out.append(md_section(
        "OSV-Scanner — dependency vulnerabilities (JS + Go)",
        tool_status["OSV-Scanner"], len(osv),
        "_Blocks on CVSS ≥ 7.0; low/medium are informational._\n\n"
        + md_table(osv, [("severity", "Severity"), ("id", "Advisory"),
                         ("location", "Package"), ("fixed", "Fixed in"),
                         ("title", "Summary")])))
    gvc_body = md_table(gvc, [("severity", "Severity"), ("id", "Advisory"),
                              ("location", "Location"), ("title", "Summary")])
    if gvc_txt:
        gvc_body += ("\n<details><summary>Full report (call traces + fix versions)</summary>\n\n"
                     f"```\n{gvc_txt.strip()}\n```\n</details>\n")
    out.append(md_section("govulncheck — reachable Go vulnerabilities",
                          tool_status["govulncheck"], len(gvc), gvc_body))
    npm_totals = (", ".join(f"{k}: {npm_meta.get(k,0)}"
                            for k in ("critical", "high", "moderate", "low")) if npm_meta else "")
    out.append(md_section(
        "npm audit — JS/TS dependency vulnerabilities",
        tool_status["npm audit"], len(npm),
        (f"_Totals — {npm_totals}. Blocks on high+._\n\n" if npm_totals else "")
        + md_table(npm, [("severity", "Severity"), ("id", "Package"),
                         ("fixed", "Fix available"), ("title", "Advisories")])))
    out.append(md_section("Semgrep OSS — SAST (informational)",
                          tool_status["Semgrep"], len(semgrep),
                          md_table(semgrep, [("severity", "Severity"), ("id", "Rule"),
                                             ("location", "Location"), ("title", "Message")])))
    out.append(md_section("gosec — Go SAST (informational)",
                          tool_status["gosec"], len(gosec),
                          md_table(gosec, [("severity", "Severity"), ("id", "Rule"),
                                           ("location", "Location"), ("title", "Message")])))
    out.append(
        f"{RESULT_EMOJI.get(gl,'➖')} **Gitleaks — secret scanning:** {gl or 'not run'} "
        "(any leak fails the build; see the Gitleaks job log for matched paths).\n")
    out += ["", "_A styled HTML version is attached as the **security-report** artifact._", ""]
    return "\n".join(out)


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--artifacts", required=True)
    ap.add_argument("--out", default="security-report.html")
    ap.add_argument("--summary", default="")
    ap.add_argument("--status", default="")
    ap.add_argument("--repo", default="")
    ap.add_argument("--sha", default="")
    ap.add_argument("--ref", default="")
    ap.add_argument("--run-url", default="")
    build(ap.parse_args())
