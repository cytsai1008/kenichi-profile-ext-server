Here is the compressed version:

---

# Remote Gallery Asset Sync Plan

## Summary

Use remote asset host as canonical source for originals + optimized files. Draft written around gallery, commissions, art collection flow �X closest match to existing repo structure. Final scope not locked; discuss before implementation. Remote files don't need same path as `src/assets/commissions`. Originals private to sync host; site serves only viewer + thumb assets.

## Architecture

- Public gallery files stay on external domain.
- Private sync endpoints on separate host e.g. `sync.kenichi-explicit.photocat.blue`.
- Public + private traffic terminate on different ports; access rules separated at service boundary, not just route checks.
- Public URLs:
  - viewer: `https://kenichi-explicit.photocat.blue/_viewer/<storedPath>`
  - thumbs: `https://kenichi-explicit.photocat.blue/_thumbs/<storedPath>`
- Private endpoints behind Cloudflare Zero Trust Access:
  - `GET /_manifest/gallery-explicit.json`
  - `GET /_files/originals/<relativePath>`
  - `PUT /_upload/originals/<relativePath>`
  - `PUT /_upload/viewer/<relativePath>`
  - `PUT /_upload/thumbs/<relativePath>`
  - `DELETE /_files/originals/<relativePath>`
  - `DELETE /_files/viewer/<storedPath>`
  - `DELETE /_files/thumbs/<storedPath>`

Remote path mirrors current gallery asset org style. Example:

- local concept: `src/assets/commissions/Baka_inuta.jpg`
- private original storage: `_originals/commissions/Baka_inuta.jpg`
- public viewer: `_viewer/commissions/Baka_inuta.abcd1234.jpg`
- public thumb: `_thumbs/commissions/Baka_inuta.abcd1234.jpg`

Don't over-separate namespaces. Keep remote assets in one pool unless need appears. To avoid filename collisions, `relativePath` needs stable logical namespace prefix when needed, not public site proxy prefix.

Example collision-safe logical paths:

- `gallery-explicit/Baka_inuta.jpg`
- `gallery-explicit/artist-a/avatar.png`

Public proxy prefix = separate delivery concern. Example:

- MD/frontmatter logical path: `gallery-explicit/Baka_inuta.jpg`
- final site URL, if proxied: `/media/gallery-explicit/_viewer/Baka_inuta.abcd1234.jpg`

For generated viewer + thumb, use hashed filenames not overwrite stable output. Manifest = source of truth mapping logical asset path to generated file paths.

Example:

- logical asset path: `gallery-explicit/Baka_inuta.jpg`
- generated viewer file path is derived from `relativePath`
- generated thumb file path is derived from `relativePath`
- example viewer URL: `_viewer/Baka_inuta.abcd1234.jpg`
- example thumb URL: `_thumbs/Baka_inuta.abcd1234.jpg`

## Public Site Proxy Path

- To expose gallery assets under main site domain, use Cloudflare edge proxy not Astro middleware.
- Don't use `src/middleware.ts`; site effectively prerendered static, locale handling already client-side in `src/components/BaseHead.astro`.
- Preferred implementation:
  - keep the real asset origin on `kenichi-explicit.photocat.blue`
  - expose a same-site proxy path on `kenichi.photocat.blue`
  - proxy via dedicated Cloudflare Worker route, not Astro middleware
- Recommended route shape:
  - use an asset-only prefix such as `/media/...` or `/assets/...`
  - avoid `/gallery/...`; site uses `/gallery` as page route
- Exact public proxy path not fixed in plan.
- During impl, confirm final public path prefix before wiring URLs + Worker routes.
- Example only, not locked in:
  - public site URL: `https://kenichi.photocat.blue/media/gallery-explicit/commissions/Baka_inuta.jpg`
  - upstream asset URL: `https://kenichi-explicit.photocat.blue/_viewer/commissions/Baka_inuta.abcd1234.jpg`
- Worker behavior:
  - match only the chosen public proxy prefix
  - strip that prefix from the request path
  - proxy `GET` and `HEAD` requests to `kenichi-explicit.photocat.blue`
  - preserve or set cache headers for image responses
  - avoid forwarding cookies or auth headers upstream unless needed
- Snippets possible for light logic, but dedicated Worker preferred �X easier to version, deploy, extend with cache + path-guard logic.

## Repo Changes

- Keep Astro site at repo root, add top-level `proxy-worker/` folder in same repo.
- Don't move site into `site/` folder.
- Keep proxy Worker isolated from Astro. Not under `src/` or `public/`; Astro code must not import from it.
- Recommended layout:
  - root: current Astro site, existing `astro.config.mjs`, current `wrangler.jsonc`
  - `proxy-worker/`: separate `wrangler.jsonc`, separate `package.json`, Worker entrypoint such as `src/index.js`
- Split gallery asset handling into 2 files:
  - `src/lib/galleryAssetShared.ts`
  - Shared path mapping, hash + cache helpers, manifest parsing, reusable image metadata helpers.
  - `src/lib/galleryAssetManifest.ts`
  - Reads `node_modules/.astro/gallery-explicit-build-manifest.json`, exposes lookup helpers for gallery entries.
- Add a prebuild script such as `scripts/gallery-explicit-sync.mjs`.
- Keep `src/content/gallery/*.md` as source for gallery metadata: title, description, artist, category, dates.
- For explicit-content gating, use dedicated boolean `explicit: true` in frontmatter; don't add generic tag system for single behavior.
- Nested folders e.g. `src/content/gallery/commissions/explicit/` fine for author org.
- Folder placement alone must not be behavior switch. Explicit handling from frontmatter field; moving files between folders must not silently change runtime behavior.
- Scope note: draft uses gallery flow as example; final scope still open, confirm before implementation.
- Update gallery image field strategy in one of two ways:
  - preferred: `image` in frontmatter = stable logical asset path with collision-safe namespace prefix when needed
  - fallback: keep current frontmatter shape, translate repo-local paths to logical remote asset path during build
- Markdown authoring rule for remote assets:
  - don't write full remote URL in frontmatter
  - don't write public proxy path in frontmatter
  - don't write hashed viewer/thumb filenames in frontmatter
  - write only stable logical asset path
- Example frontmatter value:
  - `image: gallery-explicit/Baka_inuta.jpg`
- Resolution flow:
  - markdown frontmatter stores `image`
  - sync reads remote manifest, writes `node_modules/.astro/gallery-explicit-build-manifest.json`
  - Astro looks up logical path from frontmatter in generated manifest
  - generated manifest provides final viewer + thumb URLs to render
- Update `src/components/pages/GalleryPage.astro` to resolve gallery items from generated manifest instead of `import.meta.glob()` over `/src/assets`.
- For same-site URLs, make generated manifest output proxy path on `kenichi.photocat.blue` instead of explicit domain.
- Optionally update `new-commission-post.mjs` to write logical gallery asset path format instead of repo-local relative path.
- Don't assume photo collection permanently out of scope. Reconfirm target scope before implementation.
- Proxy Worker stays in same Git repo but as separate Cloudflare Worker project with own config + deploy target.

## Remote Host

- Use separate `kenichi-explicit` repo for remote image host.
- Deploy on Linode with Docker Compose.
- Run `cloudflared` inside same Compose stack.
- Don't add separate reverse proxy container.
- Persist originals, viewer files, thumbs, manifest on Docker-mounted host volume for survival across container recreation + independent backup.
- Recommended implementation choice: Go.
- Reason for choosing Go:
  - lower memory usage on a 1 GB Linode
  - good fit for file streaming, hashing, static serving, atomic manifest updates
  - simple single-binary container
- Service shape:
  - one Go codebase
  - two app containers from the same image
  - one public port
  - one private port
  - one mounted data directory
  - one Docker image
  - one `cloudflared` container in the same Compose project
- Hostname split:
  - `kenichi-explicit.photocat.blue` �� public container + port
  - `sync.kenichi-explicit.photocat.blue` �� private container + port
- Recommended internal split:
  - public service on port `8080`
  - private service on port `8081`
- Permission model:
  - public container: viewer + thumb reads only
  - private container: manifest, original download, upload, delete routes
  - private routes unreachable on public port
  - originals not readable on public port
- Suggested on-disk layout:
  - `/data/gallery-explicit.json`
  - `/data/_originals/commissions/...`
  - `/data/_viewer/commissions/...`
  - `/data/_thumbs/commissions/...`
- Recommended Docker volume strategy:
  - mount `/srv/kenichi-explicit/data` into containers as `/data`
  - canonical persistent storage for:
    - originals
    - generated viewer files
    - generated thumbs
    - manifest JSON
  - treat container-local filesystem as disposable
- Backup expectation:
  - host-mounted data dir = primary recoverable copy on Linode
  - include in normal server backup/snapshot
  - container rebuilds/upgrades must not remove `/data`
- Go service responsible for:
  - static serving of public viewer + thumb files on public port
  - protected original downloads for build sync on private port
  - upload streaming to temp files on private port
  - SHA-256 calculation during upload
  - atomic manifest updates
  - safe delete of old hashed derivatives after manifest switch
- Recommended Compose services:
  - `explicit-public`
  - `explicit-private`
  - `cloudflared`
- `explicit-public` + `explicit-private` use same image, different startup mode/env; exposed routes differ by port + role.
- `cloudflared` should map:
  - `kenichi-explicit.photocat.blue` -> `http://explicit-public:8080`
  - `sync.kenichi-explicit.photocat.blue` -> `http://explicit-private:8081`

## Local Development

This section is for the agent implementing the `kenichi-explicit` Go server. The sync script and proxy Worker need a local server to test against end-to-end without a live Linode instance.

### Go server dev mode

The Go server must support a dev mode enabled by `--dev` flag or `GALLERY_DEV_MODE=true` env var. When dev mode is active:

- Serve a canned `gallery-explicit.json` manifest with a few test entries (e.g. `gallery-explicit/test-image.jpg`) pointing at small placeholder hashed filenames.
- Serve small placeholder JPEG bytes (e.g. 1×1 pixel) for `/_viewer/<path>` and `/_thumbs/<path>` requests on the public port (8080). Path does not need to match a real file — return the placeholder for any path.
- Accept `PUT /_upload/originals/<path>`, `PUT /_upload/viewer/<path>`, `PUT /_upload/thumbs/<path>` on the private port (8081): print the received path and byte count to stdout, return a JSON response with a dummy `hash` and `size` matching what the client sent, but do not write anything to disk.
- Accept `GET /_files/originals/<path>` on the private port: return small dummy JPEG bytes regardless of path.
- Accept `GET /_manifest/gallery-explicit.json` on the private port: return the same canned manifest as above.
- Accept `DELETE /_files/viewer/<path>` and `DELETE /_files/thumbs/<path>` on the private port: return 204 without doing anything.
- **Skip Ed25519 signature verification** in dev mode — accept any `x-signature` value, or skip the check entirely. This lets the sync script run with a dummy or absent `GALLERY_SIGNING_KEY`.
- Still require `CF-Access-Client-Id` / `CF-Access-Client-Secret` headers to be present in dev mode (any non-empty value accepted), so the credential guard in the sync script can pass without real Access credentials. Or allow disabling the check via a separate `GALLERY_DEV_SKIP_AUTH=true` env.

### Recommended local dev stack

Add a `docker-compose.dev.yml` (or `--profile dev` services) to the `kenichi-explicit` repo:

```yaml
services:
  explicit-public:
    build: .
    environment:
      - GALLERY_DEV_MODE=true
    ports:
      - "8080:8080"

  explicit-private:
    build: .
    environment:
      - GALLERY_DEV_MODE=true
    ports:
      - "8081:8081"
```

### Proxy Worker local dev

With the Go dev server running, start the proxy Worker against it:

```bash
cd proxy-worker
wrangler dev --var UPSTREAM_OVERRIDE:http://localhost:8080
```

The Worker will proxy `http://localhost:8788/alt-media/...` → `http://localhost:8080/...`.

### Sync script local dev

Point the sync script at the local private port:

```bash
GALLERY_SYNC_HOST=http://localhost:8081 \
GALLERY_CF_CLIENT_ID=dev \
GALLERY_CF_CLIENT_SECRET=dev \
npm run gallery:sync:dry
```

With `GALLERY_DEV_MODE=true` on the server (signature verification skipped), any `GALLERY_SIGNING_KEY` value (or absent) will work.

## Build Flow

1. `gallery-explicit-sync` calls protected manifest endpoint via Cloudflare Access with service-token headers.
2. Manifest returns gallery asset file list with:
   - `relativePath`
   - `sourceHash`
3. `gallery-explicit-sync` compares items against local cache in `node_modules/.astro/gallery-explicit-cache.json`.
4. If `sourceHash` + `transformVersion` match cache, skip original download + regeneration.
5. If original changed or is missing locally, download it from `GET /_files/originals/<relativePath>` into `node_modules/.astro/gallery-explicit-src/`.
6. Generate optimized viewer and thumb files into `node_modules/.astro/gallery-explicit-out/`.
7. Compute deterministic hashed output filenames from generated file bytes.
8. Compare generated hashed filenames against current values in remote manifest.
9. If unchanged, skip upload.
10. If changed, upload new hashed derivatives to protected upload endpoints.
11. Verify upload succeeded from API response hash.
12. Update remote manifest entry so logical asset path points to new hashed viewer + thumb filenames.
13. Delete old hashed viewer + thumb files only after new uploads + manifest update succeed.
14. Write `node_modules/.astro/gallery-explicit-build-manifest.json`.
15. `astro build` runs; gallery pages resolve remote asset URLs from generated manifest.

## Manual Original Upload Workflow

- Upload originals to server before site build.
- Recommended: small CLI uploader e.g. `npm run gallery:push-originals -- <paths...>`.
- CLI behavior:
  - accepts one or more local image paths
  - derives remote `relativePath` from:
    - input path relative to configured local gallery import root, or
    - an explicit `--prefix` or `--remote-path` option
  - uploads each file to `https://sync.kenichi-explicit.photocat.blue/_upload/originals/<relativePath>`
  - computes `sourceHash` locally before upload
  - accepts metadata flags when useful for asset registration
  - updates remote manifest entry after upload
  - verifies server response hash matches locally computed hash
  - does not delete existing viewer/thumb files
- Recommended CLI command shape:
  - `npm run gallery:push-originals -- .\incoming\Baka_inuta.jpg`
  - `npm run gallery:push-originals -- .\incoming\*.png --prefix artwork`
- Server-side behavior for original upload:
  - store originals under `_originals/<relativePath>`
  - preserve the exact filename and subpath
  - reject overwrite unless `--force` or explicit replace flag
  - return stored hash, byte size, and canonical storage path
- After original upload:
  - trigger `gallery-explicit-sync` in CI automatically, or
  - let next normal site build pick up new manifest entry
- Original deletion via protected admin path:
  - `DELETE /_files/originals/<relativePath>`
- Optional admin CLI:
  - `npm run gallery:delete-original -- <relativePath>`
- Original deletion behavior:
  - remove original from `_originals/<relativePath>`
  - remove or mark inactive matching manifest entry
  - don't leave manifest entry pointing at missing original
- If deletion fully removes asset, delete workflow may also remove referenced viewer + thumb files after manifest entry removed.
- Original deletion is manual or admin-only.
- No build, sync, or auto cache cleanup may delete originals.
- Generated file deletion = sync workflow, not manual upload workflow.
- During `gallery-explicit-sync`, sync script may call:
  - `DELETE /_files/viewer/<storedPath>`
  - `DELETE /_files/thumbs/<storedPath>`
- Only delete old generated files after:
  - replacement hashed files uploaded successfully
  - manifest updated successfully
- Optional later addition:
  - `gallery:delete-asset` admin CLI can be added later for cleanup, not required for initial workflow
- Better alternative for less manual manifest handling:
  - CLI = only write path for originals + metadata
  - server updates `gallery-explicit.json` automatically from upload request
  - better than manual manifest editing; avoids drift between originals + manifest entries
- Not recommended:
  - manual SCP/SFTP
  - manual manifest edits on server
  - browser-only upload UI
- Browser admin page only worth adding later for occasional uploads from CLI-inconvenient devices. For current setup, CLI = simplest + most reliable.

## Caching

- Local cache files:
  - `node_modules/.astro/gallery-explicit-cache.json`
  - `node_modules/.astro/gallery-explicit-src/`
  - `node_modules/.astro/gallery-explicit-out/`
  - `node_modules/.astro/gallery-explicit-build-manifest.json`
- `gallery-explicit-cache.json` stores per `relativePath`:
  - `sourceHash`
  - `transformVersion`
  - `viewerHash`
  - `thumbHash`
  - `viewerFileName`
  - `thumbFileName`
  - local original and output file paths
  - upload verification state
- No separate remote hash-index endpoint.
- Manifest = comparison source.
- Bump `transformVersion` when resize/quality rules change; derivatives regenerate cleanly without re-downloading unchanged originals.
- Persist `node_modules/.astro` between CI builds; sync becomes no-op when nothing changed.
- Use immutable cache headers for hashed viewer + thumb files.
- Don't expose private originals on public hostname.
- Don't overwrite existing hashed derivatives. Upload new, update manifest, then delete old unreferenced.
- Manifest shape stay minimal:
  - `relativePath` = stable namespaced logical path
  - store hashed viewer + thumb filenames, not full namespaced paths
  - build final viewer + thumb URLs by combining:
    - viewer or thumb prefix
    - directory part of `relativePath`
    - hashed filename

## Cloudflare Zero Trust

- Use Cloudflare Access Service Auth, not browser login.
- Build client sends:
  - `CF-Access-Client-Id`
  - `CF-Access-Client-Secret`
- Cloudflare Access protects only the sync API host.
- Public gallery files stay outside Access; site must serve them normally.
- Origin still validate normalized paths + restrict uploads to expected dirs.
- Add second auth layer on top of Cloudflare Access: SSH-like public/private request signing.
- Use Ed25519 signatures over HTTPS:
  - build client holds private key
  - server stores only public key
  - server verifies signature on every private manifest, upload, delete request
- Required signed-request headers:
  - `x-key-id`
  - `x-timestamp`
  - `x-nonce`
  - `x-content-sha256`
  - `x-signature`
- Signature payload:
  - HTTP method
  - request path
  - timestamp
  - nonce
  - body hash
- Server-side verification rules:
  - reject invalid signatures
  - reject timestamps outside a short skew window
  - reject nonce replays within retention window
  - reject body-hash mismatch
- Doesn't replace Cloudflare Access. Defense-in-depth: requests must pass both:
  - Cloudflare service-token auth
  - Ed25519 signed-request verification
- Private routes support:
  - `PUT` for originals, viewer, thumbs
  - `DELETE` for originals when asset intentionally removed
  - `DELETE` for old viewer + thumb files when no longer referenced by manifest
- `DELETE` should only happen after successful upload and manifest update, never before.

## Result

- Draft preserves existing gallery metadata flow, moves asset resolution to shared helper files.
- Remote hash index is removed.
- Build only downloads, optimizes, uploads changed assets within agreed scope.
- Viewer + thumb URLs = manifest-driven hashed files; long-lived caching safe.
- Static Astro rebuilds when entries/asset refs change within agreed scope, but expensive image work cached + skipped when unchanged.
