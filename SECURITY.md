# Security

Anyone who can reach the listener can upload and download. There is no
account system. That is the product. A path that leaves `uploads/` or
`assets/` is not.

## Reporting

Do not open a public issue for:

- reading a file outside `uploads/` and `assets/`
- overwriting a permanent asset via a crafted upload name
- a way to skip the 10 GB cap or the eviction lock
- a dump of `.ips.json` or `.downloads.json` from a live host

Use a
[private vulnerability advisory](https://github.com/erfnzdeh/erfanzadeh.ir/security/advisories/new).
Say the request. Do not attach someone else's upload.

## In scope

- Path traversal on upload or download
- Writes that escape the configured directories
- Unauthenticated actions beyond the documented public API
- Trusting `X-Forwarded-For` / `X-Real-Ip` in a way that lets a client
  store an arbitrary IP *and* use that for something other than the
  public list (the list already shows whatever we stored)

## Out of scope

- The server being reachable without a password. Put it behind your own
  reverse proxy if you want that.
- `/files/` including uploader IPs. That is current behaviour.
- `/stats` being public. Same.
- A client spoofing `X-Forwarded-For` so the stored IP is a lie. We
  record a header; we do not authenticate it.
- Files older than seven days being evicted when the quota is hit.

## Operator notes

Do not expose this on the public internet if the files or the IP list
would hurt anyone. `uploads/`, `assets/.downloads.json`, and
`assets/.ips.json` are runtime data. They are not secrets in the token
sense, but they are other people's files and addresses. Keep them out
of git.
