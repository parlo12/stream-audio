# Stream Audio Deployment Guide

## Quick Reference

| Environment | Compose File | Use Case | Database |
|-------------|-------------|----------|----------|
| **Local Dev** | `docker-compose.local.yml` | macOS/Windows development | Internal PostgreSQL container |
| **Production** | `docker-compose.prod.yml` | Linux server deployment | External PostgreSQL (DigitalOcean) |
| **Legacy** | `docker-compose.yml` | Production (requires /opt dirs) | External PostgreSQL |

---

## Local Development Setup (Recommended for macOS/Windows)

### Prerequisites
- Docker Desktop installed
- `.env` file configured with API keys

### Steps

```bash
# 1. Clone the repository
cd /path/to/stream-audio

# 2. Copy environment file
cp .env.example .env
# Edit .env and add your API keys (OPENAI_API_KEY, XI_API_KEY, STRIPE_SECRET_KEY, etc.)

# 3. Build all services
docker compose -f docker-compose.local.yml build

# 4. Start all services
docker compose -f docker-compose.local.yml up -d

# 5. Check status
docker compose -f docker-compose.local.yml ps

# 6. View logs
docker compose -f docker-compose.local.yml logs -f
```

### Access Services

- **Auth Service**: http://localhost:8082
- **Content Service**: http://localhost:8083
- **PostgreSQL**: localhost:5433 (username: rolf, database: streaming_db)
- **Redis**: localhost:6380

### Common Commands

```bash
# Stop all services
docker compose -f docker-compose.local.yml down

# Stop and remove volumes (fresh start)
docker compose -f docker-compose.local.yml down -v

# Rebuild specific service
docker compose -f docker-compose.local.yml build content-service
docker compose -f docker-compose.local.yml up -d content-service

# View specific service logs
docker compose -f docker-compose.local.yml logs -f content-service

# Execute command in container
docker compose -f docker-compose.local.yml exec content-service sh
```

---

## Production Deployment (Linux Server)

### Prerequisites
- Linux server (Ubuntu/Debian recommended)
- Docker and Docker Compose installed
- External PostgreSQL database (e.g., DigitalOcean Managed Database)
- Domain name configured (optional)
- `.envproduction` file with production credentials

### Initial Setup

```bash
# 1. SSH into your server
ssh user@your-server.com

# 2. Clone repository
git clone https://github.com/your-repo/stream-audio.git
cd stream-audio

# 3. Create persistent data directories
sudo mkdir -p /opt/stream-audio-data/{audio,covers,uploads,redis}
sudo chown -R $USER:$USER /opt/stream-audio-data

# 4. Configure environment
cp .env.example .envproduction
nano .envproduction  # Edit with production values
cp .envproduction .env
```

### Production Environment Variables

Ensure your `.env` file has:

```bash
# Database (External - DigitalOcean)
DB_HOST=your-db-host.db.ondigitalocean.com
DB_PORT=25060
DB_USER=doadmin
DB_PASSWORD=your-secure-password
DB_NAME=defaultdb
DB_SSLMODE=require
PGSSLMODE=require

# Redis (Internal)
REDIS_URL=redis://redis:6379

# API Keys
OPENAI_API_KEY=sk-...
XI_API_KEY=...
ELEVENLABS_VOICE_ID=...

# Stripe
STRIPE_SECRET_KEY=sk_live_...
STRIPE_WEBHOOK_SECRET=whsec_...

# JWT
JWT_SECRET=your-very-long-random-secret-key

# MQTT
MQTT_BROKER=tcp://mqtt-broker:1883
MQTT_USERNAME=your-username
MQTT_PASSWORD=your-password

# Mode
GIN_MODE=release
LOG_LEVEL=info
```

### Deploy

```bash
# 1. Build services
docker compose -f docker-compose.prod.yml build --no-cache

# 2. Start services
docker compose -f docker-compose.prod.yml up -d

# 3. Check status
docker compose -f docker-compose.prod.yml ps

# 4. View logs
docker compose -f docker-compose.prod.yml logs -f

# 5. Check health
curl http://localhost:8082/health
curl http://localhost:8083/health
```

### Production Updates

```bash
# Pull latest changes
git pull origin main

# Rebuild and restart services
docker compose -f docker-compose.prod.yml up -d --build --force-recreate

# Check logs for errors
docker compose -f docker-compose.prod.yml logs -f
```

### Monitoring

```bash
# View all logs
docker compose -f docker-compose.prod.yml logs -f

# View specific service
docker compose -f docker-compose.prod.yml logs -f content-service

# Check container stats
docker stats

# Check disk usage
du -sh /opt/stream-audio-data/*
```

---

## Key Differences: Local vs Production

| Feature | Local Development | Production |
|---------|------------------|------------|
| **Compose File** | `docker-compose.local.yml` | `docker-compose.prod.yml` |
| **Database** | Internal PostgreSQL container | External PostgreSQL (DigitalOcean) |
| **Database SSL** | Disabled (`DB_SSLMODE=disable`) | Required (`DB_SSLMODE=require`) |
| **Volumes** | Docker-managed | Bind mounts to `/opt/stream-audio-data/` |
| **Ports** | Non-standard (5433, 6380) | Standard (5432 internal, 6379 internal) |
| **GIN Mode** | Debug | Release |
| **Logging** | Console | JSON files with rotation |
| **API Keys** | Development keys | Production keys |

---

## Troubleshooting

### Port Conflicts (Local)

If you see "port already allocated" errors:

```bash
# Check what's using the ports
lsof -i :5433
lsof -i :6380
lsof -i :8082
lsof -i :8083

# Stop conflicting containers
docker ps
docker stop <container-id>
```

### Volume Permission Issues (Production)

```bash
# Fix permissions
sudo chown -R $USER:$USER /opt/stream-audio-data
chmod -R 755 /opt/stream-audio-data
```

### Database Connection Issues

```bash
# Test database connection
docker compose -f docker-compose.local.yml exec content-service sh
# Inside container:
apt-get update && apt-get install -y postgresql-client
psql -h postgres -U rolf -d streaming_db
```

### Service Won't Start

```bash
# Check logs
docker compose -f docker-compose.local.yml logs content-service

# Check if Calibre is installed
docker compose -f docker-compose.local.yml exec content-service which ebook-convert

# Rebuild from scratch
docker compose -f docker-compose.local.yml down -v
docker compose -f docker-compose.local.yml build --no-cache
docker compose -f docker-compose.local.yml up -d
```

### Audio Processing Issues

```bash
# Check FFmpeg installation
docker compose -f docker-compose.local.yml exec content-service which ffmpeg

# Check Calibre installation
docker compose -f docker-compose.local.yml exec content-service which ebook-convert

# Test MOBI conversion manually
docker compose -f docker-compose.local.yml exec content-service sh
ebook-convert test.mobi test.txt
```

---

## Supported Book Formats

| Format | Status | Implementation |
|--------|--------|----------------|
| PDF | ✅ Supported | rsc.io/pdf library |
| TXT | ✅ Supported | Native Go |
| EPUB | ✅ Supported | ZIP extraction |
| MOBI | ✅ Supported | Calibre's ebook-convert |
| AZW | ✅ Supported | Calibre's ebook-convert |
| AZW3 | ✅ Supported | Calibre's ebook-convert |
| KFX | ❌ Not Supported | No available libraries |

---

## Security Notes

### Local Development
- Uses weak default passwords (change in `.env`)
- SSL disabled for database connections
- Debug mode enabled (shows detailed errors)
- Not suitable for public access

### Production
- Strong passwords required
- SSL/TLS enforced for database
- Release mode (minimal error exposure)
- Logs stored securely
- API keys stored in environment variables

---

## Backup Strategy

### Database Backup (Production)

```bash
# Backup PostgreSQL (if using managed service, use their backup tools)
# For local PostgreSQL:
docker compose -f docker-compose.local.yml exec postgres pg_dump -U rolf streaming_db > backup.sql

# Restore
docker compose -f docker-compose.local.yml exec -T postgres psql -U rolf streaming_db < backup.sql
```

### File Storage Backup (Production)

```bash
# Backup audio files and covers
tar -czf stream-audio-backup-$(date +%Y%m%d).tar.gz /opt/stream-audio-data/

# Restore
tar -xzf stream-audio-backup-20250128.tar.gz -C /
```

---

## Performance Optimization

### For Heavy Usage

1. **Scale services**:
```bash
docker compose -f docker-compose.prod.yml up -d --scale content-service=3
```

2. **Monitor resources**:
```bash
docker stats
df -h /opt/stream-audio-data
```

3. **Clean up old files**:
```bash
# Remove temporary files older than 7 days
find /opt/stream-audio-data/uploads -type f -mtime +7 -delete
```

4. **Optimize PostgreSQL**:
- Increase connection pool size
- Add read replicas for heavy read workloads
- Enable query caching

---

## Support

For issues or questions:
- Check logs: `docker compose logs -f`
- Review [claude.md](claude.md) for architecture details
- Check Docker status: `docker compose ps`
- Verify environment variables: `docker compose config`
