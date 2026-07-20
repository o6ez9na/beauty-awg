"use client";

// A blank list should still say what this screen is for and what to do next.
// `icon` is a short glyph, not an illustration — the panel's visual language is
// hairlines and monospace, and a large graphic would sit oddly in it.
export default function EmptyState({
  icon,
  title,
  body,
  action,
}: {
  icon: string;
  title: string;
  body: string;
  action?: React.ReactNode;
}) {
  return (
    <div className="emptystate" role="status">
      <div className="emptystate-icon" aria-hidden="true">
        {icon}
      </div>
      <h3>{title}</h3>
      <p>{body}</p>
      {action && <div className="emptystate-action">{action}</div>}
    </div>
  );
}
