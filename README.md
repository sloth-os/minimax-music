# minimax-music

A Go HTTP API service that generates and downloads music via the
[MiniMaxi / Hailuo Audio](https://www.minimaxi.com) web API, with optional
**HTTP / SOCKS5 proxy** support for all outbound traffic (REST, WebSocket, and
CDN audio download).

The protocol (request signing, WebSocket streaming, audio download) was
reverse-engineered from the browser front-end; the `yy` request signature is
reproduced and verified against captured traffic in `internal/minimax/sign.go`.

## Features

- **Generate music** over the MiniMaxi WebSocket (`music-3.0` / `music-2.6`),
  collecting streamed MP3 chunks and the final `audio_url`.
- **Download** generated audio from the CDN (plain unauthenticated GET).
- **History** and **model info** REST endpoints.
- **Proxy support**: route every outbound connection (REST, WS, CDN) through an
  HTTP CONNECT proxy or a SOCKS5 proxy (`socks5`/`socks5h`).
- Two generation modes: **SSE streaming** (incremental audio chunks) and a
  simple **wait-for-final-JSON** mode.
- **Official API schema**: `POST /v1/music_generation` implements the MiniMax
  platform API OpenAPI schema (`api.md`) — `GenerateMusicReq`/`GenerateMusicResp`
  with both `stream` and non-stream modes — backed by the web client.

## Build

```bash
go build -o minimax-music .
```

Requires Go 1.24+. Dependencies: `github.com/gorilla/websocket`,
`golang.org/x/net` (SOCKS5), `gopkg.in/yaml.v3`.

## Docker

A published multi-arch image (`linux/amd64` + `linux/arm64`) is built and
pushed to GHCR on every push to `main` and on `v*` tags (workflow:
`.github/workflows/docker.yml`).

```bash
# Pull the published image (latest from main):
docker pull ghcr.io/sloth-os/minimax-music:latest

# Run it — config is env-first, no config file needed in the image:
docker run -d -p 8080:8080 \
  -e MINIMAX_TOKEN=<jwt> \
  -e MINIMAX_UUID=<browser-uuid> \
  ghcr.io/sloth-os/minimax-music:latest
```

Tags: `:latest` and `:main` track the default branch; `:1.2.3`/`:1.2`/`:1`
are produced from `v1.2.3` tags; `:sha-<short>` pins a commit. PRs build the
image (validating the multi-arch matrix) but never publish.

The image is a static Go binary on a non-root `distroless` base (no shell).
Build it locally for both architectures with Buildx (no per-arch C toolchain —
Go cross-compiles natively):

```bash
docker buildx build --platform linux/amd64,linux/arm64 -t minimax-music .
```

## Configure

Copy `config.example.yaml` to `config.yaml` and fill in your values, or use
environment variables (see `.env.example`). Env vars override the file.

The only strictly required value is **`minimax.token`** — the JWT the browser
stores in `localStorage` under `_token` on `https://www.minimaxi.com`. You
should also set `minimax.uuid` to the same browser session's uuid (visible in
any request's query string in DevTools).

### Inbound authentication

By default the service is **open**: anyone who can reach it can generate and
download music using your configured MiniMax token. To gate inbound requests,
set **`auth.api_key`** (`MINIMAX_AUTH_API_KEY`). When set, every request must
carry the header:

```
Authorization: Bearer <api_key>
```

- Missing or mismatched tokens are rejected (`401` on `/api/*`; the official
  `/v1/music_generation` endpoint answers `200` with
  `base_resp.status_code = 1004` per its schema instead).
- `/healthz` stays open so liveness probes are not gated on credentials.
- The comparison is constant-time. Brute-forcing a short key is trivial — use a
  long random string.
- With no key configured, auth is off (the historical default) and the
  `/v1/music_generation` schema check still requires the header's *presence*
  for conformance without validating its value.

```yaml
auth:
  api_key: "long-random-string"
```

### Proxy

Set `proxy.proxy_url` (or `MINIMAX_PROXY_URL`) to route all traffic through a
proxy:

```yaml
proxy:
  proxy_url: "socks5h://127.0.0.1:1080"   # remote DNS (recommended)
  # proxy_url: "http://user:pass@host:8080"
```

Supported schemes: `http`, `https`, `socks5` (local DNS), `socks5h` (proxy DNS).
Leave empty for a direct connection.

## Run

```bash
./minimax-music -config config.yaml
# or with env vars:
MINIMAX_TOKEN=... MINIMAX_UUID=... MINIMAX_PROXY_URL=socks5h://127.0.0.1:1080 ./minimax-music
```

## API

### `POST /v1/music_generation` — official MiniMax platform API schema

This endpoint implements the **official MiniMax Music Generation API** schema
(see `api.md`, OpenAPI 3.1.0). It accepts a `GenerateMusicReq` and returns a
`GenerateMusicResp`, translating internally to the reverse-engineered web
client. This lets spec-generated clients (and the official docs) talk to the
service unchanged.

**Headers** (required by the schema):

- `Content-Type: application/json`
- `Authorization: Bearer <api_key>` — required for schema conformance. This
  service wraps the web client (which authenticates with a configured JWT, not
  the caller's key), so the key gates access to this service only and is not
  forwarded upstream. When inbound auth is configured (`auth.api_key`) the token
  must match it; otherwise its presence is required for schema conformance but
  the value is not validated.

**Body** (`GenerateMusicReq`, only `model` is required):

```json
{
  "model": "music-3.0",
  "prompt": "Indie folk, melancholic, introspective",
  "lyrics": "[verse]\nStreetlights flicker...\n[chorus]\nPushing the wooden door...",
  "stream": false,
  "output_format": "hex",
  "audio_setting": { "sample_rate": 44100, "bitrate": 256000, "format": "mp3" },
  "lyrics_optimizer": false,
  "is_instrumental": false
}
```

Field rules (from `api.md`):

| field | type | rule |
| --- | --- | --- |
| `model` | string | **required**. One of `music-3.0`, `music-2.6`, `music-cover`, `music-3.0-free`, `music-2.6-free`, `music-cover-free`. |
| `prompt` | string | Text-to-music: 0–2000 (1–2000 if `is_instrumental`). Cover: 10–300, required. |
| `lyrics` | string | Non-instrumental text-to-music: 1–3500, required (unless `lyrics_optimizer`). Cover: 10–1000, optional. |
| `stream` | bool | Default `false`. When `true`, `output_format` must be `hex`. |
| `output_format` | string | `url` or `hex` (default `hex`). `url` links expire in 24h. |
| `audio_setting` | object | Optional. `sample_rate` ∈ {16000,24000,32000,44100}, `bitrate` ∈ {32000,64000,128000,256000}, `format` ∈ {mp3,wav,pcm}. **Note:** the web client always outputs 44100 Hz / 256 kbps / MP3, so requesting any other value is rejected with `2013` rather than silently ignored. |
| `lyrics_optimizer` | bool | Auto-generate lyrics from `prompt` when `lyrics` is empty. Text-to-music models only. |
| `is_instrumental` | bool | Instrumental (no vocals); `lyrics` not required. Text-to-music models only. |
| `audio_url` / `audio_base64` | string | Cover reference audio. Exactly one of the two. Mutually exclusive with `cover_feature_id`. |
| `cover_feature_id` | string | Cover two-step workflow. Mutually exclusive with `audio_url`/`audio_base64`; requires `lyrics` (10–1000). |

> **Cover models** (`music-cover`, `music-cover-free`) are accepted by the
> schema validator but are **not supported** by this service — the underlying
> web client is text-to-music only. A cover request that passes structural
> validation returns `base_resp.status_code = 2013` with an explanatory
> `status_msg`. Use `music-3.0` or `music-2.6` instead.

**Non-streaming response** (`output_format: hex`, default):

```json
{
  "data": { "status": 2, "audio": "<hex-encoded mp3>" },
  "base_resp": { "status_code": 0, "status_msg": "success" },
  "trace_id": "04ede0ab069fb1ba8be5156a24b1e081",
  "extra_info": {
    "music_duration": 25364, "music_sample_rate": 44100,
    "music_channel": 2, "bitrate": 256000, "music_size": 813651
  },
  "analysis_info": null
}
```

With `output_format: url`, `data.audio` holds the CDN `audio_url` instead.

**Streaming response** (`stream: true`, `output_format: hex`):
`text/event-stream` of `GenerateMusicResp` frames — intermediate frames carry
`data.status = 1` with a hex-encoded audio fragment; the final frame carries
`data.status = 2` with the complete hex audio, `trace_id`, and `extra_info`.

```
event: message
data: {"data":{"status":1,"audio":"<hex fragment>"},"base_resp":{"status_code":0,"status_msg":"streaming"},"analysis_info":null}

event: message
data: {"data":{"status":2,"audio":"<full hex>"},"base_resp":{"status_code":0,"status_msg":"success"},"trace_id":"...","extra_info":{...},"analysis_info":null}
```

**Errors**: per the spec, the operation declares only an HTTP `200` response,
so business errors are conveyed in `base_resp.status_code` inside the body
(HTTP `200`). `status_code` values: `0` success, `1002` rate limit, `1004`
auth failed, `1008` insufficient balance, `1026` sensitive content, `2013`
invalid params, `2049` invalid API key. Missing/wrong `Content-Type`, missing
`Authorization`, malformed JSON, unknown fields, and validation failures all
return `200` with the appropriate `base_resp.status_code` (1004 for auth,
2013 for the rest). The only non-200 response is `405` for a wrong HTTP method
(outside the declared POST operation).

Example:

```bash
curl -sX POST http://localhost:8080/v1/music_generation \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer dummy-key' \
  -d '{"model":"music-3.0","prompt":"chill lo-fi","lyrics":"la la la"}' \
  | jq '.data.audio' | xxd -r -p > music.mp3   # decode hex to mp3
```

### `POST /api/generate` — stream generation (SSE)

Body:

```json
{
  "model": "music-3.0",
  "idea": "氛围感",
  "lyrics": "cat",
  "title": "",
  "n": 1,
  "generation_type": 1
}
```

Response is `text/event-stream` with three event types:

- `chunk` — `{ "size": N, "data": "<base64 mp3>" }` (one per streamed audio chunk)
- `done`  — `{ "trace_id": "...", "items": [ {music_id, audio_url, ...} ] }`
- `error` — `{ "error": "..." }`

### `POST /api/generate/wait` — single JSON response

Same body. Returns the final result as one JSON object. Add `?include_audio=1`
to include the concatenated streamed MP3 as `audio_base64`.

### `GET /api/download?url=<audio_url>&filename=music.mp3`

Streams the MP3 from the CDN as `audio/mpeg`. `url` is the `audio_url` returned
by `/api/generate` or `/api/history`.

### `GET /api/history?page=1&page_size=20`

Lists previously generated tracks.

### `GET /api/models`

Lists available models and trial quota.

### `GET /healthz`

Liveness probe.

## Examples

Generate and save to a file (wait mode):

```bash
curl -sX POST http://localhost:8080/api/generate/wait \
  -H 'Content-Type: application/json' \
  -d '{"model":"music-3.0","idea":"chill lo-fi","lyrics":"la la la","n":1}' \
  | jq '.items[0].audio_url'
```

Download the generated track:

```bash
curl -sG http://localhost:8080/api/download \
  --data-urlencode 'url=https://cdn.hailuoai.com/.../xxx.mp3' \
  -o music.mp3
```

Stream generation and write audio incrementally:

```bash
curl -N -X POST http://localhost:8080/api/generate \
  -H 'Content-Type: application/json' \
  -d '{"model":"music-3.0","idea":"epic orchestral","n":1}'
```

## How it works

- **Signing** (`internal/minimax/sign.go`): every request carries an `yy`
  signature. For REST it is a header; for the WebSocket it is a query param.
  Algorithm:
  `yy = md5(encodeURIComponent(path+"?"+query) + "_" + bodyJSON + md5(unix) + "ooui")`
  where `bodyJSON` is `"{}"` for GET/WS and the raw POST body for POST, and the
  millisecond `unix` timestamp is itself MD5-hashed before concatenation. The
  query is emitted in the browser's **insertion order** (not alphabetically
  sorted) — `buildCommonParams` preserves the order
  `device_platform, app_id, version_code, biz_id, uuid, lang, [device_id],
  os_name, browser_name, ...` because the signature is computed over the exact
  query string sent and the server compares against traffic signed in that
  order (verified against the captured HAR: sorted order produces a different,
  server-rejected signature).
- **Generation** (`internal/minimax/generate.go`): opens `wss://.../v1/api/music/ws`
  with the signed query, sends a `MusicGen` message, and reads hex-encoded MP3
  chunks (`data[].audio`) until `ended=true` (or an item reaches `status=2` with
  an `audio_url`). Heartbeats are **server-initiated**: the server sends a
  `Heartbeat` every ~15s and the client echoes it back with the same `msg_id`
  and a fresh timestamp. The chunks are hex-decoded and concatenated; the final
  `audio_url` is also captured.
- **Download** (`internal/minimax/download.go`): plain GET to the CDN
  `audio_url` with a `Range: bytes=0-` header; no auth required.
- **Proxy** (`internal/proxy/proxy.go`): builds an `*http.Client` and a
  `*websocket.Dialer` that share the same proxy dialer, so REST, WS, and CDN
  traffic all route through it.

## Project layout

```
main.go                      # entry point, wiring, graceful shutdown
internal/config/             # YAML + env config loading
internal/proxy/              # HTTP / SOCKS5 proxy client & WS dialer
internal/minimax/            # MiniMaxi client: signing, REST, WS generate, download
internal/api/                # HTTP handlers
config.example.yaml
.env.example
```

## Notes / caveats

- This is an unofficial client targeting the web front-end, not a public API.
  Endpoints and the signing scheme may change without notice.
- The token is a long-lived JWT tied to a browser session; treat it as a secret.
- `socks5h://` (proxy-side DNS) is recommended over `socks5://` to avoid local
  DNS leaks.
