# Kergui landing page

Next.js (App Router) exported as a static site (`out/`), served by nginx.

```sh
npm ci
npm run dev        # http://localhost:3000
npm run build      # writes out/
npm run lint && npm run typecheck
```

## Deploy on Dokploy

- Application → source: this repo, **Build type: Dockerfile**, build path `landing/`, Dockerfile `Dockerfile`.
- Container port: **80**. Domain: the site's domain, HTTPS on.
- Build arguments (read at build time, so redeploy after changing them):

| Argument | Example | Use |
|---|---|---|
| `NEXT_PUBLIC_SITE_URL` | `https://kergui.sn` | canonical links, sitemap, Open Graph |
| `NEXT_PUBLIC_HUB_URL` | `https://hub.kergui.sn` | the « Prendre un pass » button (`/licensing/v1/buy`) |
| `NEXT_PUBLIC_PLAY_URL` | Play Store link | shows the Google Play button (empty: hidden) |
| `NEXT_PUBLIC_APK_URL` | APK download link | defaults to the GitHub latest release |

Content (offers, FAQ, links) lives in `lib/site.ts`. Prices are shown on the web
only; the Play app itself shows none (V1 rule).
