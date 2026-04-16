# kenichi-explicit-server

Go HTTP server that acts as the remote asset host for the `kenichi-profile` gallery build pipeline.

Two containers run from the same binary image:

| Container          | Hostname                              | Port | Role                                                              |
|--------------------|---------------------------------------|------|-------------------------------------------------------------------|
| `explicit-public`  | `kenichi-explicit.photocat.blue`      | 8080 | Serve viewer + thumb images publicly                              |
| `explicit-private` | `sync.kenichi-explicit.photocat.blue` | 8081 | Manifest CRUD, original upload/download, derivative upload/delete |

The private port is reachable only through Cloudflare Zero Trust Access (service-token auth), with Ed25519 request
signing as a second layer.

## How it fits in the build pipeline

```
gallery-explicit-sync.mjs (prebuild)
  │
  ├─ GET  /_manifest/gallery-explicit.json   ← fetch current manifest
  ├─ GET  /_files/originals/<rel>            ← download originals that changed
  ├─ PUT  /_upload/viewer/<storedPath>       ← upload new viewer derivative
  ├─ PUT  /_upload/thumbs/<storedPath>       ← upload new thumb derivative
  ├─ PUT  /_manifest/gallery-explicit.json   ← update manifest entry (new hashed filenames)
  └─ DELETE /_files/viewer/<old>             ← clean up superseded files
     DELETE /_files/thumbs/<old>

gallery-push-originals.mjs (manual / CI)
  └─ PUT  /_upload/originals/<rel>           ← auto-registers entry in manifest
```

The Astro site reads `node_modules/.astro/gallery-explicit-build-manifest.json` (written by the sync script) — it never
talks to this server directly.

## On-disk layout

```
/data/
├── gallery-explicit.json          ← manifest (atomic writes)
├── _originals/
│   └── gallery-explicit/
│       └── Baka_inuta.jpg
├── _viewer/
│   └── gallery-explicit/
│       └── Baka_inuta.abcd1234.jpg
└── _thumbs/
    └── gallery-explicit/
        └── Baka_inuta.abcd1234.jpg
```

## Manifest format

```json
{
  "version": 1,
  "entries": [
    {
      "relativePath": "gallery-explicit/Baka_inuta.jpg",
      "sourceHash": "e3b0c442...",
      "viewerFile": "Baka_inuta.abcd1234.jpg",
      "thumbFile": "Baka_inuta.abcd1234.jpg",
      "updatedAt": "2026-04-16T00:00:00Z"
    }
  ]
}
```

- `relativePath` — stable collision-safe logical path used as the manifest key
- `viewerFile` / `thumbFile` — just the filename (no path); full URL = `_viewer/<dir of relativePath>/<viewerFile>`
- `sourceHash` — SHA-256 hex of the stored original; drives cache invalidation in the sync script

## Private API

All private endpoints require:

- `CF-Access-Client-Id` / `CF-Access-Client-Secret` headers (Cloudflare Access service token)
- Ed25519 signed-request headers: `x-key-id`, `x-timestamp`, `x-nonce`, `x-content-sha256`, `x-signature`

### Endpoints

| Method   | Path                               | Description                                    |
|----------|------------------------------------|------------------------------------------------|
| `GET`    | `/_manifest/gallery-explicit.json` | Full manifest                                  |
| `PUT`    | `/_manifest/gallery-explicit.json` | Upsert one entry (JSON body)                   |
| `GET`    | `/_files/originals/{path...}`      | Download original                              |
| `PUT`    | `/_upload/originals/{path...}`     | Upload original; auto-registers in manifest    |
| `PUT`    | `/_upload/viewer/{path...}`        | Upload viewer derivative                       |
| `PUT`    | `/_upload/thumbs/{path...}`        | Upload thumb derivative                        |
| `DELETE` | `/_files/originals/{path...}`      | Delete original + mark manifest entry inactive |
| `DELETE` | `/_files/viewer/{path...}`         | Delete old viewer file                         |
| `DELETE` | `/_files/thumbs/{path...}`         | Delete old thumb file                          |

Upload response:

```json
{
  "hash": "<sha256hex>",
  "size": 12345
}
```

Originals reject overwrite unless `?force=true`.

## Auth

### Cloudflare Access

The private hostname is protected at the edge by a Cloudflare Access policy using a service token. The server checks
that `CF-Access-Client-Id` and `CF-Access-Client-Secret` are non-empty (the actual token validation happens at the CF
edge before the request reaches origin).

### Ed25519 request signing

Defense-in-depth on top of CF Access. The sync script signs every private request:

```
payload = METHOD + "\n" + path + "\n" + timestamp + "\n" + nonce + "\n" + sha256hex(body)
signature = Ed25519.sign(privateKey, payload)
```

The server verifies the signature, rejects timestamps outside ±120 s, and rejects replayed nonces (retained 10 min in
memory).

Generate a keypair:

```bash
node scripts/gen-keypair.mjs
```

Set the outputs:

- `GALLERY_SIGNING_KEY` → CI / build environment (private key)
- `GALLERY_ED25519_PUBLIC_KEY` → server environment (public key, base64)

## Environment variables

| Variable                     | Default              | Description                               |
|------------------------------|----------------------|-------------------------------------------|
| `GALLERY_ROLE`               | *(required)*         | `public` or `private`                     |
| `GALLERY_DATA_DIR`           | `/data`              | Mounted volume root                       |
| `GALLERY_PUBLIC_PORT`        | `8080`               | Public server port                        |
| `GALLERY_PRIVATE_PORT`       | `8081`               | Private server port                       |
| `GALLERY_DEV_MODE`           | `false`              | Enable dev mode (see below)               |
| `GALLERY_DEV_SKIP_AUTH`      | `false`              | Skip CF-Access header check in dev mode   |
| `GALLERY_ED25519_PUBLIC_KEY` | *(required in prod)* | Base64-encoded 32-byte Ed25519 public key |

## Local development

Start both containers without auth secrets or a real data volume:

```bash
docker compose -f docker-compose.dev.yml up --build
```

Point the sync script at the local private server:

```bash
GALLERY_SYNC_HOST=http://localhost:8081 \
GALLERY_CF_CLIENT_ID=dev \
GALLERY_CF_CLIENT_SECRET=dev \
npm run gallery:sync:dry
```

In dev mode:

- `GET /_manifest/...` returns a canned manifest with one test entry
- `GET /_viewer/...` and `GET /_thumbs/...` return a 1×1 placeholder JPEG for any path
- `PUT /_upload/...` prints the received path/size to stdout, returns a dummy hash — no disk writes
- Ed25519 signature verification is skipped entirely
- `GALLERY_DEV_SKIP_AUTH=true` also skips the CF-Access header check

## Production deployment

### Prerequisites

- Linode (or equivalent) with Docker + Docker Compose
- Cloudflare tunnel configured for both hostnames
- A `.env` file alongside `docker-compose.yml`:

```env
CLOUDFLARE_TUNNEL_TOKEN=<token>
GALLERY_ED25519_PUBLIC_KEY=<base64 public key>
```

### Deploy

```bash
# First run — create the data directory on the host
sudo mkdir -p /srv/kenichi-explicit/data

docker compose up -d --build
```

### Tunnel config (Cloudflare dashboard)

```
kenichi-explicit.photocat.blue      → http://explicit-public:8080
sync.kenichi-explicit.photocat.blue → http://explicit-private:8081
```

The private hostname must be protected by a Cloudflare Access policy (service token).

### Backup

The host path `/srv/kenichi-explicit/data` contains everything that must survive a container rebuild: originals, viewer
files, thumb files, and the manifest JSON. Include it in your normal server backup/snapshot. Container-local storage is
disposable.

## Building locally

```bash
go build -o server .
GALLERY_ROLE=private GALLERY_DEV_MODE=true GALLERY_DEV_SKIP_AUTH=true ./server
```

Requires Go 1.22 or later. No external dependencies — stdlib only.
