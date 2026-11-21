// Throwaway mock of sdrd for UI testing: node scripts/mock-sdrd.mjs  (port 8073)
import http from "node:http";
import { WebSocketServer } from "ws";

const meta = { type: "meta", center_hz: 433920000, sample_rate: 2048000, bins: 2048, min_db: -40, max_db: 60, fps: 20 };
const status = {
  device: "Mock RTL-SDR", tuner: "R820T", gains_db: [0, 0.9, 1.4], center_hz: meta.center_hz,
  sample_rate: meta.sample_rate, gain_db: null, fft_size: meta.bins, fps: meta.fps, mode: "spectrum", connected: true,
};

const server = http.createServer((req, res) => {
  if (req.url === "/api/status") {
    res.setHeader("content-type", "application/json");
    return res.end(JSON.stringify(status));
  }
  if (req.url === "/api/tune" && req.method === "POST") {
    let body = "";
    req.on("data", (c) => (body += c));
    req.on("end", () => {
      const p = JSON.parse(body || "{}");
      if (p.freq_hz) status.center_hz = meta.center_hz = p.freq_hz;
      if (p.sample_rate) status.sample_rate = meta.sample_rate = p.sample_rate;
      if ("gain_db" in p) status.gain_db = p.gain_db;
      for (const c of wss.clients) c.send(JSON.stringify(meta));
      res.setHeader("content-type", "application/json");
      res.end(JSON.stringify(status));
    });
    return;
  }
  res.statusCode = 404;
  res.end();
});

const wss = new WebSocketServer({ server, path: "/ws/spectrum" });
wss.on("connection", (ws) => ws.send(JSON.stringify(meta)));

let t = 0;
setInterval(() => {
  t++;
  const n = meta.bins;
  const buf = Buffer.alloc(n);
  const toV = (db) => Math.max(0, Math.min(255, Math.round(((db - meta.min_db) * 255) / (meta.max_db - meta.min_db))));
  for (let i = 0; i < n; i++) {
    let db = -30 + (Math.random() - 0.5) * 6;
    for (const [pos, amp] of [[0.3, 45], [0.72, 30 + 10 * Math.sin(t / 10)]]) {
      const d = (i / n - pos) * n;
      db = Math.max(db, -30 + amp * Math.exp(-(d * d) / 18));
    }
    buf[i] = toV(db);
  }
  const mid = n >> 1; buf[mid] = buf[mid - 1];
  for (const c of wss.clients) if (c.readyState === 1) c.send(buf);
}, 1000 / meta.fps);

server.listen(8073, "127.0.0.1", () => console.log("mock sdrd on 127.0.0.1:8073"));
