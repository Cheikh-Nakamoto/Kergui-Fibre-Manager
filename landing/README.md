# Kergui website: landing page and back-office

One Next.js app (App Router, `output: 'standalone'`):

- `/`: the landing page, prerendered at build time (SEO: metadata, Open Graph,
  sitemap, robots, JSON-LD).
- `/admin`: the back-office for the Kergui team (issue #62). It is rendered on
  the server, never indexed, and for platform administrators only. The Next
  server calls the hub's `/api/v1/admin/*` with the admin's session, kept in
  httpOnly cookies (page scripts never see the tokens). Sections: hub health,
  households and members, passes and payments (with a pass granted by hand,
  audited), and the WhatsApp session (link, unlink).
- « Passes et paiements » (`/admin/passes`, issue #97) also lists the passes
  that expire (ending within 7 days, or ended less than 30 days ago; hub
  `GET /api/v1/admin/passes/expiring?status=soon|expired&tier=basic|max&q=`)
  and the renewal figures of a month (hub
  `GET /api/v1/admin/passes/renewals?month=YYYY-MM`): the passes whose end
  date falls in the month, renewed (more days, before or after the end) or
  lost, the rate renewed / (renewed + lost), the Basic and Max passes running,
  the trials converted, and the lost households. Renewals are recorded by the
  hub from the version that ships this page on (a database trigger on the
  subscriptions); before that, only the ends the J-3 WhatsApp reminder
  announced are known, and the page says so for those months. The 12 months
  of revenue come from `GET /api/v1/admin/payments/revenue?by=month`.

```sh
npm ci
KERGUI_HUB_URL=http://localhost:8090 npm run dev   # http://localhost:3000
npm run build && npm run lint && npm run typecheck
```

## Back-office accounts

A back-office account is a hub account with the platform role `ROLE_ADMIN`.
Owners of households never get that role. Create one on the hub's host (it only
writes to the database, so the running hub is not disturbed):

```sh
docker exec -e KERGUI_ADMIN_PASSWORD='at least 12 characters' <hub container> /kergui-hub admin create you@example.org
```

Run it again on an existing account to promote it, or to replace its password.

## Deploy on Dokploy

- Application → source: this repo, **Build type: Dockerfile**, build path `landing/`.
- Container port: **3000**. Domain: the site's domain, HTTPS on.
- **Environment** (runtime): `KERGUI_HUB_URL`, the hub as this container
  reaches it (its internal Dokploy address, or its public URL).
- **Build arguments** (baked into the landing page, so redeploy after changing them):

| Argument | Example | Use |
|---|---|---|
| `NEXT_PUBLIC_SITE_URL` | `https://kergui.sn` | canonical links, sitemap, Open Graph |
| `NEXT_PUBLIC_HUB_URL` | `https://hub.kergui.sn` | the « Prendre un pass » button (`/licensing/v1/buy`) |
| `NEXT_PUBLIC_PLAY_URL` | Play Store link | shows the Google Play button (empty: hidden) |
| `NEXT_PUBLIC_BUY_ENABLED` | `true` | shows « Prendre un pass » (only once the hub's licensing, NabooPay and WhatsApp are on) |
| `NEXT_PUBLIC_APK_URL` | a direct `.apk` link | optional: by default the button hits `/telecharger`, which downloads the newest APK of the GitHub releases |

The content of the landing page (offers, FAQ, links) lives in `lib/site.ts`.
Prices are shown on the web only: the Play app itself shows none (V1 rule).
