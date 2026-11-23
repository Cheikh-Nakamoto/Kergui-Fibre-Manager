# Kergui website: the landing page (prerendered) and the back-office (/admin),
# one small Next.js server. Dokploy builds this file.
FROM node:24-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
# Baked into the landing page at build time (links, canonical URL).
ARG NEXT_PUBLIC_SITE_URL
ARG NEXT_PUBLIC_HUB_URL
ARG NEXT_PUBLIC_PLAY_URL
ARG NEXT_PUBLIC_APK_URL
ENV NEXT_TELEMETRY_DISABLED=1 \
    NEXT_PUBLIC_SITE_URL=$NEXT_PUBLIC_SITE_URL \
    NEXT_PUBLIC_HUB_URL=$NEXT_PUBLIC_HUB_URL \
    NEXT_PUBLIC_PLAY_URL=$NEXT_PUBLIC_PLAY_URL \
    NEXT_PUBLIC_APK_URL=$NEXT_PUBLIC_APK_URL
RUN npm run build

FROM node:24-alpine
WORKDIR /app
ENV NODE_ENV=production NEXT_TELEMETRY_DISABLED=1 PORT=3000 HOSTNAME=0.0.0.0
RUN addgroup -S web && adduser -S web -G web
COPY --from=build --chown=web:web /app/.next/standalone ./
COPY --from=build --chown=web:web /app/.next/static ./.next/static
COPY --from=build --chown=web:web /app/public ./public
USER web
EXPOSE 3000
# KERGUI_HUB_URL (runtime): the hub as this server reaches it, for /admin.
HEALTHCHECK CMD wget -q -O /dev/null http://127.0.0.1:3000/ || exit 1
CMD ["node", "server.js"]
