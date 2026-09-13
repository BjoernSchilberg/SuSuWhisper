# README

SuSuWhisper is a minimalist and anonymous platform for creating and publishing
texts, similar to Telegra.ph. It allows articles to be written and shared
quickly without the need to register or log in.


## Docker

```shell
docker-compose up -d --build
```

The port is published on `127.0.0.1` only. Put a reverse proxy such as Caddy
in front of it to serve the site with HTTPS:

```
susuwhisper.example.org {
    reverse_proxy 127.0.0.1:8080
}
```


## Configuration

All settings are optional environment variables.

| Variable | Default | Meaning |
|---|---|---|
| `SUSU_ADDR` | `127.0.0.1:8080` | Listen address. Use `:8080` inside a container. |
| `SUSU_STORAGE_QUOTA_MB` | `1024` | Space for articles and uploads together. |
| `SUSU_RATE_LIMIT` | `120` | Articles and uploads per client IP and minute, `0` disables the limit. |
| `SUSU_TRUST_PROXY` | `false` | Set to `true` behind a reverse proxy so the rate limit uses the client IP from `X-Forwarded-For`. |

Many users behind one school or company network share a single IP address,
so keep the rate limit generous there.


## Security

- Article HTML is filtered (bluemonday); scripts, event handlers and
  `javascript:` links are removed, also from articles saved earlier.
- Uploads must be PNG, JPEG, GIF or WebP images, detected from the file
  content, at most 10 MB each. They are stored under random file names.
- Published articles cannot be overwritten, and no files can be added to them.


## Tests

Requires Go 1.26; `go.mod` pins the toolchain, so an older local Go
downloads it automatically.

```shell
go test .
```
