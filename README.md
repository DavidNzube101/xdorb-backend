# XDOrb Backend - Xandeum pNode Analytics API

A high-performant backend API for **[XDOrb](https://xdorb.appwrite.network)** - the Xandeum pNode analytics dashboard.

## Features

- **RESTful API**: Clean REST endpoints for pNode data
- **Redis Caching**: Distributed caching with fallback to in-memory
- **API Key Authentication**: Secure frontend-only access
- **Rate Limiting**: Token bucket rate limiting
- **Health Checks**: Comprehensive health monitoring
<!-- - **Mock pRPC Integration**: Ready for real Xandeum pRPC connection -->

## API Endpoints

### Public Endpoints
- `GET /health` - Health check (no auth required)

### Protected Endpoints (require API key)
- `GET /api/dashboard/stats` - Dashboard statistics
- `GET /api/pnodes` - List pNodes with filters
- `GET /api/pnodes/:id` - pNode details
- `GET /api/pnodes/:id/history` - Historical metrics
- `GET /api/pnodes/:id/peers` - Connected peers
- `GET /api/pnodes/:id/alerts` - pNode alerts
- `GET /api/leaderboard` - Top performers
- `GET /api/network/heatmap` - Network heatmap data

## Security

- API key authentication required for all endpoints except health
- Rate limiting (100 requests/minute per IP)
- Input validation and sanitization
- CORS configuration for frontend domain

### Access

- For Access, reach out to developer: [Skipp | Telegram](https://t.me/skipp_dev) or [Skipp | X](https://x.com/DavidNzubee)