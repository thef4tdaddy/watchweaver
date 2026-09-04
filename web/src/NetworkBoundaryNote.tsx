type NetworkBoundaryNoteProps = {
  badge?: boolean;
};

export default function NetworkBoundaryNote({
  badge = false,
}: NetworkBoundaryNoteProps) {
  if (badge) {
    return <span className="network-boundary-badge">PRIVATE LAN / VPN ONLY</span>;
  }
  return (
    <div className="network-boundary-note">
      <strong>Private network required</strong>
      <p>
        WatchWeaver has no application login. Use it only on a trusted LAN,
        through a private VPN, or behind an authenticated reverse proxy.
      </p>
    </div>
  );
}
