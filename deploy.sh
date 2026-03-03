#!/bin/bash
set -e

# Trandor Deployment Script
# Usage: ./deploy.sh

SERVER="root@167.99.250.110"
APP_DIR="/opt/trandor"

echo "==> Deploying Trandor to $SERVER"

# 1. Ensure server has Docker
echo "==> Checking Docker installation..."
ssh $SERVER "which docker || (curl -fsSL https://get.docker.com | sh)"

# 2. Stop and remove any old satilligence containers (legacy cleanup)
echo "==> Cleaning up legacy containers..."
ssh $SERVER "docker stop satilligence-caddy-1 satilligence-web-1 satilligence-api-1 2>/dev/null || true"
ssh $SERVER "docker rm satilligence-caddy-1 satilligence-web-1 satilligence-api-1 2>/dev/null || true"

# 3. Fix DNS if needed (use Google DNS as fallback)
echo "==> Ensuring DNS resolution works..."
ssh $SERVER "grep -q '8.8.8.8' /etc/resolv.conf || echo 'nameserver 8.8.8.8' >> /etc/resolv.conf"

# 4. Create app directory
echo "==> Creating app directory..."
ssh $SERVER "mkdir -p $APP_DIR"

# 5. Copy files to server
echo "==> Copying files..."
rsync -avz --exclude='.git' --exclude='node_modules' --exclude='tmp' --exclude='log' \
    ./ $SERVER:$APP_DIR/

# 6. Copy production env file
echo "==> Setting up environment..."
scp .env.production $SERVER:$APP_DIR/.env

# 7. Build and start containers
echo "==> Building and starting containers..."
ssh $SERVER "cd $APP_DIR && docker compose -f docker-compose.prod.yml up -d --build"

# 8. Show status
echo "==> Deployment complete! Checking status..."
ssh $SERVER "cd $APP_DIR && docker compose -f docker-compose.prod.yml ps"

echo ""
echo "==> Done! Site should be live at https://www.trandor.com"
echo "    (Make sure DNS is pointing to 167.99.250.110)"
