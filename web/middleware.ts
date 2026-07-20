import { NextRequest, NextResponse } from "next/server";

// The dashboard is a statically prerendered client page, so without this the
// browser receives and paints the whole shell before the first API call comes
// back 401 — a visible flash of someone else's panel. Redirecting at the edge
// means an unauthenticated visitor never receives that markup at all.
//
// This only checks that a session cookie is present; whether it is still valid
// is the backend's call. A stale cookie therefore still reaches the page, which
// is why the dashboard also holds its own render until the first load succeeds.
const SESSION_COOKIE = "awg_session";

export function middleware(req: NextRequest) {
  if (req.cookies.has(SESSION_COOKIE)) return NextResponse.next();

  const url = req.nextUrl.clone();
  url.pathname = "/login";
  return NextResponse.redirect(url);
}

export const config = {
  matcher: ["/", "/graph"],
};
