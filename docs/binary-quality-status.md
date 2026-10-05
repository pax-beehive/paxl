# Binary quality status

Update checks send current_version to the Manager resolver. A disabled installed
version is shown with an upgrade warning in text and JSON output. An already
newer installed version is not silently downgraded. The CLI updater reject a candidate carrying disabled, before downloading executable bytes.

Manager must implement the quality resolver contract. Older Managers continue
working without the optional installed-version warning. HTTP 410 reports a known
bad installed version with no available replacement. This does not stop running
clients or revoke previously downloaded binaries.
