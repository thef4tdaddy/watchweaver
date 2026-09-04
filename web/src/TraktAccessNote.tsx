type TraktAccessNoteProps = {
  compact?: boolean;
};

export default function TraktAccessNote({ compact = false }: TraktAccessNoteProps) {
  return (
    <div className={`trakt-access-note${compact ? " compact" : ""}`}>
      <strong>Trakt VIP is currently required for new API applications.</strong>
      <p>
        Existing valid Trakt applications still work. This is a Trakt policy,
        not a WatchWeaver subscription.
      </p>
      <a
        href="https://trakt.tv/oauth/applications"
        target="_blank"
        rel="noreferrer"
      >
        Open Trakt API applications ↗
      </a>
    </div>
  );
}
