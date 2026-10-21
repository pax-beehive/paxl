# Scheduled tasks

`paxl cron` manages schedules in the local paxd SQLite database through its Unix
socket. paxd owns scheduling and ACP execution. Select a registered, enabled
connection with `paxl daemon agent list`; its harness and credentials are reused.

```sh
paxl cron status
paxl cron create --name 'Daily review' --connection CONNECTION_ID \
  --schedule '0 9 * * *' --timezone America/Los_Angeles \
  --cwd /path/to/project --prompt-file ./review-prompt.txt --session-mode reuse
paxl cron list
paxl cron get JOB_ID
paxl cron update --prompt-file ./new-prompt.txt JOB_ID
paxl cron pause JOB_ID
paxl cron resume JOB_ID
paxl cron run JOB_ID
paxl cron runs --limit 20 JOB_ID
paxl cron delete JOB_ID
```

Output is JSON. `--prompt-file -` reads stdin. The prompt is stored, not reread
from its file at execution. Each queued run saves its own prompt and workspace
snapshot. `--cwd` defaults to the current directory, `--timezone` to UTC, and
`--timeout` to 1h (whole seconds, maximum 24h). `--disabled` creates a paused job.
Use `paxl cron --socket /path/to/paxd.sock list` for a custom socket and root
`--verbose` for request traces.

`--session-mode new` (default) creates a session each run. `reuse` creates and
binds one session, then resumes it. Use `paxl cron update --reset-session JOB_ID`
to replace that binding on the next run. Changing a bound directory, connection,
or mode requires reset. Target changes/reset are rejected while a run is active.
The saved workspace also pins the remote and Manager agent identity.

`runs` exposes prompt, cwd, connection, mode, status, error, native session ID,
and Manager `session_id`. Manager registration and encrypted history synchronize
after outages; a locally completed run may not have a Manager session ID yet.
The Console displays the source cronjob name and creation run on ordinary sessions.

Updates patch only supplied fields with revision conflict detection. Pause/resume
affects future scheduled work. Manual runs are allowed while paused. Deletion
retains run/session history. Five-field schedules have minute resolution. Missed
occurrences coalesce into one catch-up run; overlapping occurrences are skipped.
Work interrupted by restart is recorded and never automatically retried.

paxd sends ACP cancellation on timeout. Permissions follow the normal agent and
Console path; unanswered approvals time out. Local transcripts are durable and
history remains end-to-end encrypted in Manager. Retention is currently manual.

Install the Manager registration endpoint before enabling jobs in updated paxd.
This version supplies local CLI/API CRUD and a Console session-origin badge; it
does not import native schedules, wake machines, or provide a cron management page.
