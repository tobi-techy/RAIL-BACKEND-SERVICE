FROM oven/bun:1 AS build

WORKDIR /app
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile --production

FROM oven/bun:1-slim AS production

WORKDIR /app
COPY package.json ./
COPY --from=build /app/node_modules ./node_modules
COPY src/ ./src/

RUN mkdir -p /app/data

EXPOSE 3000

CMD ["bun", "run", "src/index.ts"]
