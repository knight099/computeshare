FROM node:24.11.1-bookworm-slim AS base
WORKDIR /app
RUN corepack enable && corepack prepare pnpm@10.20.0 --activate
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY --chown=node:node . .
ENV NEXT_TELEMETRY_DISABLED=1
RUN pnpm typecheck
USER node
CMD ["pnpm", "dev:api"]
