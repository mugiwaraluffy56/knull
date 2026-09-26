FROM node:24-alpine AS build
WORKDIR /app
RUN corepack enable && corepack prepare pnpm@10 --activate
COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY . .
ARG NEXT_PUBLIC_API_BASE_URL
ENV NEXT_PUBLIC_API_BASE_URL=${NEXT_PUBLIC_API_BASE_URL}
RUN pnpm build

FROM node:24-alpine
WORKDIR /app
RUN corepack enable && corepack prepare pnpm@10 --activate
COPY --from=build --chown=node:node /app /app
USER node
ENV NODE_ENV=production
EXPOSE 3000
CMD ["pnpm", "start"]
