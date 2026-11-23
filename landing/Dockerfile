# Kergui landing page: built once with Next.js (static export), served by nginx.
# Dokploy builds this file; set the build arguments in its "Build" settings.
FROM node:24-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
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

FROM nginx:1.29-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/out /usr/share/nginx/html
EXPOSE 80
HEALTHCHECK CMD wget -q -O /dev/null http://127.0.0.1/ || exit 1
