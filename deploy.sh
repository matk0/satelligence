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

# 2. Create app directory
echo "==> Creating app directory..."
ssh $SERVER "mkdir -p $APP_DIR"

# 3. Copy files to server
echo "==> Copying files..."
rsync -avz --exclude='.git' --exclude='node_modules' --exclude='tmp' --exclude='log' \
    ./ $SERVER:$APP_DIR/

# 4. Copy production env file
echo "==> Setting up environment..."
scp .env.production $SERVER:$APP_DIR/.env

# 5. Build and start containers
echo "==> Building and starting containers..."
ssh $SERVER "cd $APP_DIR && docker compose -f docker-compose.prod.yml up -d --build"

# 6. Show status
echo "==> Deployment complete! Checking status..."
ssh $SERVER "cd $APP_DIR && docker compose -f docker-compose.prod.yml ps"

echo ""
echo "==> Done! Site should be live at https://www.trandor.com"
echo "    (Make sure DNS is pointing to 167.99.250.110)"
