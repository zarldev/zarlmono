# Troubleshooting

The first-week failure modes. If `task doctor` reports something red,
look here for the fix; if the symptom isn't here, open an issue.

## Audio / mic

**Mic permission denied or no audio input in the browser.**

Browsers only grant `getUserMedia` to "secure contexts": HTTPS, or
HTTP on `localhost`. If you're hitting zarl from another machine on
your LAN (`http://192.168.x.x:8080`), the browser will refuse the
permission silently.

Fixes, in order of effort:

1. **Use the loopback URL** (`http://localhost:8080`) on the same
   machine that runs zarl.
2. **Tell Chromium-based browsers to trust the origin:**
   ```
   chrome --unsafely-treat-insecure-origin-as-secure=http://192.168.x.x:8080 \
          --user-data-dir=/tmp/zarl-chrome
   ```
   Don't do this in your normal browser profile.
3. **Run zarl behind a reverse proxy with TLS** — Caddy or
   `nginx` with a self-signed cert. Best long-term answer for LAN
   access.

## Audio / speaker

**No TTS audio.** Check the browser tab isn't muted, and that the
operating system isn't silencing the browser. The talking-head
animation runs even with audio muted, so a moving face with no sound
usually means the OS or tab is muted.

**Audio plays but is choppy.** The Kokoro synthesizer streams audio
in chunks. Choppiness usually means the host CPU is at 100%, often
because llama-server and the browser are competing for the same
cores. Move the LLM off the host (hosted endpoint) or pin
llama-server to a subset of cores via `cpuset_cpus:` in docker-compose.

## Camera

**Camera shows a black frame.**

- **Browser permission denied** — same secure-context rule as the mic.
- **WSL** — WSL2 cannot pass camera devices through to Linux by
  default. Capture has to happen on the Windows side: open zarl in a
  Windows browser pointed at `http://localhost:8080` (Vite dev server
  ports proxy through WSL automatically). Camera access from a Linux
  GUI inside WSL is more involved (`usbipd-win`, custom kernel) and
  usually not worth it.
- **Linux native** — confirm `/dev/video0` exists and your user is in
  the `video` group: `groups | grep -q video || sudo usermod -aG video $USER`.

## Backing services

**llama-server takes 60–120 s to start.** Loading 25 GB of GGUF onto
a GPU isn't instant. The compose healthcheck waits up to 120 s
(`start_period: 120s`). Subsequent restarts are faster because the
weights are in the OS page cache.

**`task ps` shows `qdrant: unhealthy` even when Qdrant works.** The
`qdrant/qdrant` image stopped shipping `wget`, which the compose
healthcheck still uses. Verify Qdrant another way:
```
curl -fsS http://localhost:6333/healthz
```
Returns `healthz check passed` when fine. The "unhealthy" status is
cosmetic.

**`docker compose --profile llm up` fails with "could not select
device driver".** The NVIDIA Container Toolkit isn't installed or the
Docker daemon hasn't been told about it.

```
# Ubuntu / Debian
distribution=$(. /etc/os-release; echo $ID$VERSION_ID)
curl -s -L https://nvidia.github.io/libnvidia-container/gpgkey | sudo apt-key add -
curl -s -L https://nvidia.github.io/libnvidia-container/$distribution/libnvidia-container.list | sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
sudo apt update && sudo apt install -y nvidia-container-toolkit
sudo nvidia-ctk runtime configure --runtime=docker
sudo systemctl restart docker
```

If you don't have an NVIDIA GPU at all, see *Hardware alternatives*
in the [README](../README.md) — you don't need llama-server.

**`Dolt: Access denied for user 'root'@'…'`.** The compose definition
sets `DOLT_ROOT_PASSWORD=""` and `DOLT_ROOT_HOST=%`. Your local DSN
must match: `root:@tcp(localhost:3307)/zarl?parseTime=true`. If
you've previously run zarl with a different password, drop the
`dolt-data` volume:
```
docker compose down
docker volume rm zarl_dolt-data
task up
```
(this destroys all your local DB state — take a `dolt dump` first if
you care).

## zarl runtime

**Startup error: `CHAT_URL is required`.** Either:
- `.env` is missing — `cp .env.example .env`
- The Taskfile didn't load `.env` — run via `task run`, not `./zarl`
  directly. The Taskfile's `dotenv: ['.env']` directive is what
  populates the env. If you want to run the binary outside Task,
  source `.env` yourself: `set -a; source .env; set +a; ./zarl`.

**Startup error: `deploy/models/whisper-small-en/...: no such file`.** The
STT model bundle wasn't extracted. See *Models* in the
[README](../README.md). Or set `MODELS_DIR` in `.env` to point at the
directory where you actually keep model bundles.

**The Immersive view is blank with no errors.** You don't have a
person enrolled yet. Navigate to `http://localhost:8080/onboard` and
run the wizard. Eventually the wizard step will be auto-redirect when
no person exists; today it's manual.

**Tools listed in `/admin → Tools` show "0 tools".** Tool providers
load on startup. If they failed to register (e.g. an MCP server isn't
running), the row stays at 0. Check `tail -f` of the binary's log
for `tool provider initialized` messages — providers that didn't
print one didn't load.

## Tests

**Local `go test ./repository/...` fails with `active name = "default
v8", want default`.** The test isn't isolating from prior dev-DB
state. CI is fine because each run gets a fresh Dolt container.
Locally, drop the volume between test runs:
```
docker compose down && docker volume rm zarl_dolt-data && task up
```
or run the failing test against a freshly-migrated DB.

**`go test ./repository/...` skips with `dolt not available`.** Dolt
isn't running. `task up`.

## Frontend

**`npx tsc -b` complains about `node_modules` not existing.**
`task setup` (or just `task frontend:install`) wasn't run.

**Vite dev server (`task frontend:dev`) shows blank page on
`/admin`.** The dev server proxies RPC to `:8080`. The admin views
are built atop ConnectRPC; if `task run` (the Go backend) isn't
running, every admin RPC will fail and the panels render empty.
Start the backend first.

## When you're stuck

1. Run `task doctor` — surfaces 90 % of the predictable issues.
2. Check the binary's log (`task run` writes to stdout) for
   `slog.Error` lines.
3. Check the browser devtools network tab for failed RPC calls
   (`/zarl.v1.AdminService/...` patterns).
4. Open an issue with the doctor output + the failing log lines.
