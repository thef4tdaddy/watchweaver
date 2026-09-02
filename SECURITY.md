# Security Policy

WatchWeaver is a self-hosted application that is expected to handle authentication credentials and private viewing activity. Security and privacy issues should be treated carefully even during early development.

## Supported network boundary

WatchWeaver does not provide application-native authentication. Supported deployments must restrict access to a trusted LAN, a private VPN, or an authenticated reverse proxy. Direct exposure of the WatchWeaver port to the public internet is unsupported.

Anyone who can reach the web interface can use its administrative and workflow controls. Firewall, VPN, and reverse-proxy access rules are therefore part of the required security boundary, not optional hardening.

## Reporting a vulnerability

Please do **not** open a public GitHub issue containing a vulnerability that could expose credentials, authentication tokens, private watch history, Discord access, or other sensitive information.

Until a dedicated private vulnerability-reporting channel is configured, open a minimal public issue stating that you have a security concern **without including exploit details or sensitive data** so a private communication path can be arranged.

## Never include these in reports

- Trakt client secrets, OAuth tokens, or refresh tokens
- Discord bot tokens or webhook URLs
- `.env` contents
- WatchWeaver database files
- Personal watch-history exports
- Generated exports containing private viewing activity
- Logs containing credentials or tokens

If a credential has accidentally been published, revoke/rotate it with the relevant provider immediately. Removing it from a later Git commit does not make the exposed credential safe again.

## Supported versions

WatchWeaver is currently pre-alpha and has no supported release versions. This policy will be updated when releases begin.
