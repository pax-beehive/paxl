# Secure Artifact Download BDD

## Feature: resolver and artifact requests never follow redirects

### Scenario: update resolver is intercepted by Cloudflare Access

- Given `paxl update` requests a manager resolver with an HTTP client that
  normally follows redirects
- When the resolver returns a 3xx response to an Access login page
- Then paxl stops at the resolver response
- And reports only the HTTP status
- And never requests the redirect target

### Scenario: a presigned paxl or paxd download redirects

- Given the manager returned an HTTPS object URL containing signed query
  parameters
- When the object endpoint returns a 3xx response
- Then paxl stops at that response rather than downloading the redirect target
- And the error and command output contain neither the object URL nor its query
  parameters

### Scenario: an HTTP transport embeds a signed URL in its error

- Given the Go HTTP stack or curl reports an error containing the request URL
- When paxl surfaces the resolver or download failure
- Then it emits a generic transport error
- And does not persist or print the complete signed URL

### Scenario: a public installer endpoint redirects to the released script

- Given the public manager install endpoint intentionally returns one redirect
- When a user runs the documented install command
- Then curl may follow at most that single redirect
- And the downloaded installer itself follows zero redirects for both resolver
  and signed binary requests

### Scenario: tests inject an HTTP fake

- Given a unit test supplies an `UpdateHTTPClient` fake
- When the facade performs a resolver or download request
- Then the fake remains injectable and receives the original request
- And a raw 3xx fake response is rejected as an HTTP error

## Feature: daemon lifecycle commands honor self-hosted remotes

### Scenario: no resolver override is supplied

- Given local paxd has a `default` remote with a self-hosted `cloud_api_url`
- When `paxl daemon install`, `update`, or `update check` selects no remote
- Then paxl derives `/api/v1/public/paxd/download` from that remote
- And if the implicit default remote is unavailable it retains the hosted
  fallback

### Scenario: a remote or resolver is selected explicitly

- Given local paxd has multiple remotes
- When the command supplies `--remote prod`
- Then paxl derives the resolver from the `prod` remote
- And an explicit `--resolver-url` takes precedence without loading a remote

## Feature: release verification rejects unrelated installer redirects

### Scenario: the public installer is protected by an Access login

- Given the `stable,installer` JSON resolver returns a signed object URL
- And Cloudflare service-token credentials are configured for admin publish
- When `/api/v1/public/paxl/install.sh` returns HTTP 302 to a different scheme,
  authority, or path
- Then the release fails without printing either URL or signed query
- And neither the public resolver nor installer request carries the CF service
  token or admin bearer token

### Scenario: a manager response is not valid release JSON

- Given a publish or resolver response contains HTML or malformed JSON
- When the release script parses the response
- Then it reports only the expected field name
- And never echoes the response body or source URL
