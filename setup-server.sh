#!/bin/bash
# Server setup script for stream-audio project
# Handles PostgreSQL, Redis, and existing MQTT setup

set -e

echo "🚀 Setting up Stream Audio Server with PostgreSQL support..."

# Create persistent data directories
sudo mkdir -p /opt/stream-audio-data/{audio,covers,redis,postgres,uploads}
sudo mkdir -p /opt/stream-audio/logs/{auth,content,redis,postgres,gateway}

# Set proper permissions
sudo chown -R $USER:$USER /opt/stream-audio-data
sudo chown -R $USER:$USER /opt/stream-audio/logs

# Check if MQTT is already running
if [ -d "/opt/mqtt" ]; then
    echo "✅ MQTT setup detected at /opt/mqtt"
    # Check if MQTT service is running
    if docker ps | grep -q mosquitto; then
        echo "✅ MQTT broker is running"
    else
        echo "⚠️  MQTT broker is not running. Starting it..."
        cd /opt/mqtt && docker-compose up -d
    fi
else
    echo "⚠️  MQTT setup not found at /opt/mqtt - you may need to set this up separately"
fi

# Install Docker if not installed
if ! command -v docker &> /dev/null; then
    echo "📦 Installing Docker..."
    curl -fsSL https://get.docker.com -o get-docker.sh
    sudo sh get-docker.sh
    sudo usermod -aG docker $USER
fi

# Install Docker Compose if not installed
if ! command -v docker-compose &> /dev/null; then
    echo "📦 Installing Docker Compose..."
    sudo curl -L "https://github.com/docker/compose/releases/download/v2.20.0/docker-compose-$(uname -s)-$(uname -m)" -o /usr/local/bin/docker-compose
    sudo chmod +x /usr/local/bin/docker-compose
fi

# Install PostgreSQL client tools for debugging
sudo apt update
sudo apt install -y postgresql-client

# Setup log rotation
sudo tee /etc/logrotate.d/stream-audio > /dev/null <<EOF
/opt/stream-audio/logs/*/*.log {
    daily
    missingok
    rotate 14
    compress
    delaycompress
    notifempty
    copytruncate
}
EOF

# Create systemd service for auto-restart
sudo tee /etc/systemd/system/stream-audio.service > /dev/null <<EOF
[Unit]
Description=Stream Audio Application
Requires=docker.service
After=docker.service

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=/opt/stream-audio/stream-audio
ExecStart=/usr/local/bin/docker-compose -f docker-compose.prod.yml up -d
ExecStop=/usr/local/bin/docker-compose -f docker-compose.prod.yml down
TimeoutStartSec=0

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl enable stream-audio.service

# Create environment template if it doesn't exist
if [ ! -f "/opt/stream-audio/stream-audio/.env" ]; then
    echo "📝 Creating .env template..."
    cat > /opt/stream-audio/stream-audio/.env.template << 'ENVEOF'
# Database Configuration
DB_HOST=postgres
DB_PORT=5432
DB_NAME=streamaudio
DB_USER=postgres
DB_PASSWORD=your_secure_password_here
DB_SSLMODE=require
PGSSLMODE=require

# Redis Configuration
REDIS_URL=redis://redis:6379

# Application Configuration
LOG_LEVEL=info
AUDIO_STORAGE_PATH=/app/audio

# JWT Configuration
JWT_SECRET=your_jwt_secret_here

# Stripe Configuration (for payments)
STRIPE_SECRET_KEY=sk_test_your_stripe_secret_key
STRIPE_PUBLISHABLE_KEY=pk_test_your_stripe_publishable_key

# MQTT Configuration (connecting to existing broker at /opt/mqtt)
MQTT_BROKER_HOST=localhost
MQTT_BROKER_PORT=1883
MQTT_USERNAME=your_mqtt_username
MQTT_PASSWORD=your_mqtt_password

# File Upload Configuration
MAX_FILE_SIZE=50MB
UPLOAD_PATH=/app/uploads

# Audio Processing
AUDIO_STORAGE_PATH=/app/audio

# WebSocket Configuration
WEBSOCKET_PORT=8084

# Gateway Configuration
CORS_ORIGINS=*
PORT=8080

# Optional: Cloud Storage (DigitalOcean Spaces)
# AWS_ACCESS_KEY_ID=your_spaces_access_key
# AWS_SECRET_ACCESS_KEY=your_spaces_secret_key
# STORAGE_BUCKET_NAME=stream-audio-files
ENVEOF
    echo "⚠️  Please copy .env.template to .env and configure your settings"
fi

# Create database initialization script
cat > /opt/stream-audio/stream-audio/init-db.sh << 'DBEOF'
#!/bin/bash
# Database initialization script
echo "🔄 Initializing database..."

# Wait for PostgreSQL to be ready
docker-compose -f docker-compose.prod.yml exec postgres pg_isready -U postgres

# Run database migrations if needed
echo "✅ Database is ready for migrations"

# You can add your database setup commands here
# Example: docker-compose exec auth-service ./migrate-db.sh
DBEOF

chmod +x /opt/stream-audio/stream-audio/init-db.sh

echo "✅ Server setup complete!"
echo ""
echo "📊 Services configured:"
echo "  - PostgreSQL: localhost:5432"
echo "  - Redis: localhost:6379"
echo "  - Auth Service: localhost:8082"
echo "  - Content Service: localhost:8083"
echo "  - MQTT Broker: localhost:1883 (separate service)"
echo ""
echo "📁 Persistent storage locations:"
echo "  - Audio files: /opt/stream-audio-data/audio"
echo "  - Cover images: /opt/stream-audio-data/covers"
echo "  - PostgreSQL data: /opt/stream-audio-data/postgres"
echo "  - Redis data: /opt/stream-audio-data/redis"
echo "  - Logs: /opt/stream-audio/logs"
echo ""
echo "🚀 Next steps:"
echo "1. Copy .env.template to .env and configure your credentials"
echo "2. cd /opt/stream-audio/stream-audio"
echo "3. docker-compose -f docker-compose.prod.yml up -d"
echo "4. Run ./init-db.sh to initialize the database"
echo "5. Use 'docker-compose logs -f' for real-time logs"
echo "6. Use 'sudo journalctl -u stream-audio -f' for service logs"