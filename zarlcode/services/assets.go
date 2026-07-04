package services

import "embed"

// Assets contains the Docker Compose files zarlcode can materialise for local
// tool services. These assets are optional runtime helpers, not model servers.
//
//go:embed assets/docker-compose.yml assets/searxng/settings.yml assets/searxng/limiter.toml
var Assets embed.FS
