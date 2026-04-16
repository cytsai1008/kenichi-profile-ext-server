# kenichi-explicit-server — project context for AI assistants

## What this repo is

A Go HTTP server that hosts original + optimised gallery images for the `kenichi-profile` Astro site. It is deployed as two Docker containers from the same binary, separated by role:

- **public** (port 8080) — serves viewer and thumb images from `_viewer/` and `_thumbs/` on disk. No auth. Long-lived immutable cache headers on hashed filenames.
- **private** (port 8081) — manifest read/write, original file download, upload of originals and derivatives, file deletion. Requires Cloudflare Access service-token headers + Ed25519 request signatures.

The companion repos are:
- `kenichi-profile` — the Astro site. Contains `scripts/gallery-explicit-sync.mjs` (prebuild sync), `scripts/gallery-push-originals.mjs` (manual upload CLI), and `src/lib/galleryAssetShared.ts` (shared types and signing constants).
- `kenichi-profile-ext-server` — this repo.

## Codebase layout

```
main.go                        GALLERY_ROLE dispatch → RunPublic or RunPrivate
internal/config/config.go      env var loading, Ed25519 public key decode
internal/manifest/manifest.go  atomic JSON manifest store (sync.RWMutex + os.Rename)
internal/auth/auth.go          CF-Access header guard + Ed25519 sig verification + nonce replay map
internal/server/public.go      public HTTP server (static file serving + dev placeholder)
internal/server/private.go     private HTTP server (all write/read endpoints)
internal/server/helpers.go     isValidRelPath, safeJoin, detectContentType, placeholderJPEG, devManifest
scripts/gen-keypair.mjs        one-time Ed25519 keypair generator (Node, no deps)
Dockerfile                     multi-stage: golang:1.22-alpine → alpine:3.20
docker-compose.yml             production: explicit-public, explicit-private, cloudflared
docker-compose.dev.yml         dev: same containers, GALLERY_DEV_MODE=true, no volumes
```

## Key invariants — do not break these

- **No external Go dependencies.** stdlib only (`net/http`, `crypto/ed25519`, `encoding/json`, `image/jpeg`, etc.).
- **Uploads stream to temp file** before auth verification, to avoid loading large files into RAM. Temp is deleted if auth fails. Never buffer a full upload body.
- **Atomic manifest writes** via `os.Rename(tmp, real)`. Never write to the manifest file directly.
- **safeJoin** must be called before every filepath.Join that uses user-supplied path segments. It verifies the result stays within the base directory.
- **isValidRelPath** must be called before safeJoin. It rejects empty strings, null bytes, `..` components, absolute paths, and backslashes/colons.
- **Dev mode is not a partial bypass** — it must mock all disk operations, return placeholder data, and skip both CF-Access and Ed25519 checks (when GALLERY_DEV_SKIP_AUTH=true) so the full sync flow can run without any real secrets or files.
- **Originals are never deleted by the sync workflow.** Only `DELETE /_files/originals/<rel>` (admin, manual) removes originals. The sync script only deletes old viewer/thumb derivatives.

## Auth flow

All private endpoints go through two auth layers in order:

1. **CF-Access guard** — checks `CF-Access-Client-Id` and `CF-Access-Client-Secret` are non-empty. In production, CF validates these at the edge before the request reaches the server; the server check is a belt-and-suspenders guard. Skipped when `GALLERY_DEV_SKIP_AUTH=true`.

2. **Ed25519 signature** — signed payload is `METHOD\npath\ntimestamp\nnonce\nbodyHashHex`. Max clock skew ±120 s (`SIGN_MAX_SKEW_SECONDS` in `galleryAssetShared.ts`). Nonces are stored in memory for 10 min to detect replays. Skipped when `GALLERY_DEV_MODE=true`.

For GET/DELETE requests the body is empty; auth reads and verifies before the handler runs (`withSmallBodyAuth`). For PUT uploads, auth happens **after** streaming the body (because we need to compute the body hash without buffering). The temp file is removed on auth failure.

## Manifest format

```json
{
  "version": 1,
  "entries": [
    {
      "relativePath": "gallery-explicit/Baka_inuta.jpg",
      "sourceHash":   "e3b0c442...",
      "viewerFile":   "Baka_inuta.abcd1234.jpg",
      "thumbFile":    "Baka_inuta.abcd1234.jpg",
      "updatedAt":    "2026-04-16T00:00:00Z"
    }
  ]
}
```

- `relativePath` is the manifest key. It is a collision-safe logical path including a namespace prefix (e.g. `gallery-explicit/`).
- `viewerFile` / `thumbFile` are filenames only (no path). The full URL is built by `gallery-explicit-sync.mjs` as: `_viewer/<dirOf(relativePath)>/<viewerFile>`.
- `sourceHash` is SHA-256 hex of the original. The sync script compares it to its local cache to decide whether to re-download and regenerate.
- `updatedAt` is set by the server on every `Upsert` call.

## Private endpoint contract (matches gallery-explicit-sync.mjs)

| Method | Path | Notes |
|--------|------|-------|
| GET | `/_manifest/gallery-explicit.json` | Returns full manifest JSON |
| PUT | `/_manifest/gallery-explicit.json` | Body = one `Entry` JSON object; upserts by relativePath; sets updatedAt |
| GET | `/_files/originals/{path...}` | Returns raw file bytes |
| PUT | `/_upload/originals/{path...}` | Streams body; auto-upserts manifest entry with sourceHash; rejects overwrite unless ?force=true; returns `{"hash":"<hex>","size":<n>}` |
| PUT | `/_upload/viewer/{path...}` | Streams body; returns `{"hash":"<hex>","size":<n>}`; no manifest update |
| PUT | `/_upload/thumbs/{path...}` | Same as viewer |
| DELETE | `/_files/originals/{path...}` | Removes file; manifest cleanup is caller's responsibility |
| DELETE | `/_files/viewer/{path...}` | Removes file; 204 even if not found |
| DELETE | `/_files/thumbs/{path...}` | Same as viewer |

Upload response hash is a plain hex string (not prefixed with `sha256:`). The sync script compares it directly against its locally computed hash.

## Adding new endpoints

1. Add the handler method to `privateHandler` in `internal/server/private.go`.
2. Register the pattern on `mux` in `RunPrivate`. Use Go 1.22 method+path patterns (`GET /path/{wild...}`). Use `r.PathValue("wild")` to extract wildcards.
3. For small-body endpoints (GET, DELETE, or small JSON PUT): wrap with `p.withSmallBodyAuth(...)`.
4. For streaming uploads: follow the pattern in `streamUpload` — temp file, MultiWriter hash, auth after stream.
5. Add a dev-mode stub (log + return placeholder/204) guarded by `if p.cfg.DevMode`.

## Environment variables reference

| Variable | Default | Used by |
|---|---|---|
| `GALLERY_ROLE` | *(required)* | `main.go` |
| `GALLERY_DATA_DIR` | `/data` | both servers |
| `GALLERY_PUBLIC_PORT` | `8080` | public |
| `GALLERY_PRIVATE_PORT` | `8081` | private |
| `GALLERY_DEV_MODE` | `false` | both |
| `GALLERY_DEV_SKIP_AUTH` | `false` | private |
| `GALLERY_ED25519_PUBLIC_KEY` | *(required in prod)* | private, base64 32-byte Ed25519 pubkey |

## What not to change without discussion

- The manifest JSON field names — `gallery-explicit-sync.mjs` and `galleryAssetShared.ts` depend on them.
- The upload response shape (`hash`, `size`) — the sync script checks `json.hash` by exact string comparison.
- The signing payload format — must stay `METHOD\npath\ntimestamp\nnonce\nbodyHashHex` to match the client.
- The `maxSkew` constant (120 s) — must match `SIGN_MAX_SKEW_SECONDS` in `galleryAssetShared.ts`.
- The data directory layout (`_originals/`, `_viewer/`, `_thumbs/`, `gallery-explicit.json`) — changing it requires a migration plan.
