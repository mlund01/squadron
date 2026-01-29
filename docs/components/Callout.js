export function Callout({ type = 'note', children }) {
  return <div className={`callout callout-${type}`}>{children}</div>;
}
