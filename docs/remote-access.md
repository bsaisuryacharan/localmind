# Remote access

By default localmind listens only on `localhost`. Three supported ways to reach it from elsewhere:

## 1. Tailscale (recommended)

[Install Tailscale](https://tailscale.com/download) on the localmind host and on your phone/laptop. The Web UI is then reachable at `http://<host-machine-name>:3000` from any device on your tailnet — no port forwarding, no public IP.

For sharing with one external user, enable [Tailscale Funnel](https://tailscale.com/kb/1223/funnel):

```bash
sudo tailscale funnel 3000
```

This publishes the Web UI on a public HTTPS URL. Ensure `WEBUI_AUTH=true` in `.env` first.

## 2. Cloudflare Tunnel

```bash
cloudflared tunnel --url http://localhost:3000
```

Same idea, different provider. Use a [named tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/) for stable URLs.

## 3. Reverse proxy with TLS

If you have a domain and a public host, terminate TLS in Caddy / nginx and proxy to `localhost:3000`. Sample Caddyfile:

```
ai.example.com {
    reverse_proxy localhost:3000
}
```

Always combine with `WEBUI_AUTH=true` so the Web UI requires login.
