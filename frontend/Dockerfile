# Stage 1: Build static assets on the builder's native platform (e.g. x86_64).
# JS/CSS is arch-agnostic so skipping QEMU here cuts build time massively.
FROM --platform=$BUILDPLATFORM oven/bun:1-slim AS builder

WORKDIR /app

COPY package.json bun.lock ./
RUN --mount=type=cache,target=/root/.bun/install/cache \
    bun install --frozen-lockfile

COPY . .

# Bake the API URL into the bundle. Leaving it empty makes src/config/env.ts
# fall back to "/api", which the runtime nginx layer proxies to the backend —
# so the deployed frontend never has to know the backend hostname at build time.
ARG VITE_API_URL=""
ENV VITE_API_URL=${VITE_API_URL}

RUN bun run build

# Stage 2: Runtime on target arch (arm64 for Pi).
FROM nginx:alpine-slim

COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf.template /etc/nginx/templates/default.conf.template

ENV PORT=80
ENV BACKEND_HOST=trader-backend-app
ENV BACKEND_PORT=8080

EXPOSE 80

CMD ["nginx", "-g", "daemon off;"]
