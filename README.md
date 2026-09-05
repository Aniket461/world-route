# World Route

Angular map UI + Go API. Mapbox draws the map, searches places, and optimizes the trip (TSP) from start through stops to end.

## Stack

| Piece | Local | Production |
|---|---|---|
| Frontend | `ng serve` | **Netlify** |
| Backend | `go run` or Docker | **Railway** (Docker) |
| Maps / routing | Mapbox | Mapbox |

## What you need

- [Go 1.22+](https://go.dev/dl/) and [Node.js LTS](https://nodejs.org/) for local work
- Docker (optional, for the API container)
- A Mapbox **public** (`pk.`) token

Copy `.env.example` to `.env` and set `MAPBOX_ACCESS_TOKEN`.

## Run locally

### Option A — processes

```bash
# terminal 1 — Postgres
docker compose up -d postgres

# terminal 2 — API (needs DATABASE_URL + JWT_SECRET in .env)
cd backend
go run .

# terminal 3 — UI
cd frontend
npm install
npm start
```

Open [http://localhost:4200](http://localhost:4200). Sign in is optional; use it to save trips.

### Option B — API + Postgres in Docker

```bash
docker compose up --build
cd frontend && npm start
```

## Deploy

### 1. Railway (Go API)

1. Create a new Railway project from this repo.
2. Set the **root directory** to `backend` (or point the Dockerfile at `backend/Dockerfile`).
3. Variables:
   - `MAPBOX_ACCESS_TOKEN` = your `pk.` token
   - `CORS_ORIGIN` = `https://YOUR_SITE.netlify.app` (add `http://localhost:4200` while testing; comma-separate multiple)
   - `DATABASE_URL` = Railway Postgres connection string
   - `JWT_SECRET` = a long random secret
4. Railway sets `PORT` automatically.
5. Copy the public HTTPS URL, e.g. `https://world-route-api.up.railway.app`.

Add a Railway **Postgres** plugin/service and paste its URL into `DATABASE_URL`.

Health check: `GET /api/health`

### 2. Netlify (Angular)

1. New site from this repo.
2. Netlify reads `netlify.toml` (`base=frontend`, publish `dist/frontend/browser`).
3. Site env vars:
   - `API_BASE_URL` = your Railway URL (**no trailing slash**)
4. Deploy. Then put that Netlify URL into Railway `CORS_ORIGIN` and redeploy the API if needed.

### 3. Mapbox token hygiene

- Keep using a **public** token for map + Search Box from the API.
- After you have a production domain, create a URL-restricted token for the browser map if you later split tokens.
- Never commit `.env`.

## Limits

Mapbox Optimization v1 allows **12 coordinates** per request: start, end, and at most **10** stops. Points that are not connected on the road network can fail to route.
