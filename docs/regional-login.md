# Regional login

`paxl login` starts US and HK candidates and opens one browser approval link.
The Worker resolves the authenticated user's existing home. Browser approval only
confirms identity. The CLI pins the first valid candidate in its existing private
SQLite database before committing a credential in that region.

If a commit or acknowledgement response is lost, run the same login command again.
It reuses the stored attempt and regional requests, retaining the chosen identity.
It does not switch emails or regions after an uncertain commit. A credential and
its acknowledgement receipt are saved atomically before sending the ACK.

`--manager-url` selects an explicit Manager. The hidden operator `--admin` flag
requires that explicit URL and a regional administrator account at the target.
Ordinary login cannot override the home-region assignment. New clients require
`client_commit_v1` support and do not fall back to legacy automatic issuance.

One SQLite attempt coordinates simultaneous local processes. Independent
installations, copied databases and deliberately separate attempts have no global
single-winner guarantee. No D1 data or schema is added by the login protocol.

BDD tests cover concurrent selection with different emails, reopen recovery,
lost commit/ACK responses, invalid responses, home routing failure, and rejection
of stale processes attempting to overwrite a saved credential. Run `go test ./...`
and `go test -race ./internal/model/store ./internal/facade -run
'LoginAttempt|AuthFacadeSuite'`. Release after the Worker/Console and both Managers.
