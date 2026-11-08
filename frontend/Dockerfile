# Builds the Vue app into static files and serves them with nginx, which
# also forwards /api to the Go API (see nginx.conf).

FROM node:26-alpine AS build
WORKDIR /app
# Exact versions from package-lock.json; cached until the lock file changes.
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM nginx:1.29-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
