FROM node:22-alpine AS builder

WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm ci --ignore-scripts

COPY . .
RUN npm run build

# Unprivileged nginx: runs as uid 101, keeps its pid and temp files under
# /tmp, and listens on 8080, so the pod can run as non-root with a read-only
# root filesystem and no capabilities (deploy/helm templates/console.yaml).
FROM nginxinc/nginx-unprivileged:1.27-alpine
COPY --from=builder /app/built /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 8080
CMD ["nginx", "-g", "daemon off;"]
