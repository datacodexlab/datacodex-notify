// A stand-in for the app's UI API with canned data, used by check.mjs to render
// every tab in headless Edge. It serves web/ and answers /api/v1/ui/*.
import http from "node:http";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const webRoot = path.resolve(here, "..", "..", "web");

const TYPES = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".png": "image/png",
  ".woff2": "font/woff2",
  ".webmanifest": "application/manifest+json",
};

const now = () => new Date();
const ago = (minutes) => new Date(Date.now() - minutes * 60000).toISOString();

function messages(count) {
  const agents = ["Codex", "Claude Code", "Gemini"];
  const statuses = ["read", "delivered", "sent", "failed", "queued"];
  const levels = ["info", "warning", "critical"];
  return Array.from({ length: count }, (_, i) => {
    const level = levels[i % 3];
    const emoji = { info: "\uD83D\uDFE2", warning: "\uD83D\uDFE0", critical: "\uD83D\uDEA8" }[level];
    return {
      id: `m${i}`,
      created_at: ago(i * 7 + 1),
      level,
      source: "codex",
      text: `${emoji} *${agents[i % 3]}* \u00B7 billing-api\n\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\n\uD83D\uDCCC *Phase ${i} done*\n\nMerged the branch and ran the tests.\n\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\n\uD83D\uDD50 10:${String(i % 60).padStart(2, "0")} \u00B7 21/10`,
      mode: "text",
      status: statuses[i % statuses.length],
      attempts: 1,
      last_error: statuses[i % statuses.length] === "failed" ? "connection reset" : "",
    };
  });
}

function sessions(count) {
  const sources = ["codex", "claude-code", "gemini", "other"];
  return Array.from({ length: count }, (_, i) => ({
    id: `s${i}`,
    source: sources[i % 4],
    session_name: `billing-api-k${i}f2`,
    project: i % 2 ? "billing-api" : "",
    rules_files: [],
    connected_at: ago(60 * i),
    last_seen_at: ago(5),
    last_message_at: ago(10 + i),
    muted: i === 1,
    disconnected_at: i >= 5 ? ago(60 * 3) : null,
    status: i >= 5 ? "disconnected" : i === 1 ? "muted" : "connected",
  }));
}

const DEMO_PROJECTS = ["acme-shop", "data-pipeline"];

function demoMessages() {
  const rows = [
    ["info", "Codex", "acme-shop", "Phase 3 finished", "read", 2],
    ["warning", "Claude Code", "data-pipeline", "Two tests failed", "delivered", 9],
    ["info", "Codex", "acme-shop", "Step 2 finished", "delivered", 31],
    ["critical", "Codex", "acme-shop", "Build is blocked", "read", 66],
    ["info", "Claude Code", "data-pipeline", "Schema migrated", "sent", 120],
    ["info", "Codex", "acme-shop", "Task started", "read", 190],
  ];
  const emoji = { info: "\uD83D\uDFE2", warning: "\uD83D\uDFE0", critical: "\uD83D\uDEA8" };
  const bar = "\u2501".repeat(14);
  return rows.map(([level, agent, project, title, status, minutes], i) => ({
    id: `d${i}`, created_at: ago(minutes), level, source: agent.toLowerCase(),
    text: `${emoji[level]} *${agent}* \u00B7 ${project}\n${bar}\n\uD83D\uDCCC *${title}*\n\nDetails of the step.\n${bar}\n\uD83D\uDD50 10:42 \u00B7 21/10`,
    mode: "text", status, attempts: 1, last_error: "",
  }));
}

function demoSessions() {
  return [
    { id: "ds0", source: "claude-code", session_name: "data-pipeline-x4k9", project: "data-pipeline", rules_files: [], connected_at: ago(95), last_seen_at: ago(1), last_message_at: ago(9), muted: false, disconnected_at: null, status: "connected" },
    { id: "ds1", source: "codex", session_name: "acme-shop-k7f2", project: "acme-shop", rules_files: [], connected_at: ago(240), last_seen_at: ago(1), last_message_at: ago(2), muted: false, disconnected_at: null, status: "connected" },
  ];
}

function build(scenario) {
  if (scenario === "demo" || scenario === "demo-fresh" || scenario === "demo-checklist") {
    const linked = scenario !== "demo-fresh";
    const checklist = scenario === "demo-checklist";
    return {
      state: {
        state: linked ? "connected" : "unlinked",
        has_qr: !linked,
        pairing: { active: !linked, expired: false, started: true, code_expires_at: Math.floor(Date.now() / 1000) + 48 },
        has_queued: false,
        active_heartbeats: 2,
        app_version: process.env.DCN_VERSION || "1.0.0",
        home: {
          linked_phone: linked ? "+201" + "\u2022".repeat(7) + "00" : "",
          last_sent_at: linked && !checklist ? ago(2) : null,
          recipients: checklist || !linked ? 0 : 1,
          agents_total: checklist || !linked ? 0 : 2,
          agents_connected: checklist || !linked ? 0 : 2,
          paused: false,
          pause_until: null,
        },
      },
      messages: checklist || !linked ? [] : demoMessages(),
      recipients: [{ id: "r0", name: "Ahmed Demo", phone_e164: "+201000000000", jid: "", enabled: true, levels: 7 }],
      sessions: checklist ? [] : demoSessions(),
      keys: [{ id: "k0", label: "Demo key", masked: "AIza...demo", added_at: ago(60 * 24 * 3) }],
      heartbeats: DEMO_PROJECTS.map((p, i) => ({ id: `h${i}`, label: p, source: i ? "claude-code" : "codex", session: "", interval_s: 600, grace_s: 300, last_beat_at: ago(i + 1), state: "active" })),
    };
  }

  const busy = scenario === "busy";
  const fresh = scenario === "fresh";
  const base = {
    state: "connected",
    has_qr: false,
    pairing: { active: false, expired: false, started: true, code_expires_at: 0 },
    has_queued: false,
    active_heartbeats: 2,
    app_version: process.env.DCN_VERSION || "1.0.0",
    home: {
      linked_phone: "+201\u2022\u2022\u2022\u2022\u2022\u202278",
      last_sent_at: ago(4),
      recipients: 1,
      agents_total: busy ? 8 : 2,
      agents_connected: busy ? 5 : 2,
      paused: false,
      pause_until: null,
    },
  };
  if (fresh) {
    Object.assign(base, { state: "unlinked", has_qr: true, pairing: { active: true, expired: false, started: true, code_expires_at: Math.floor(Date.now() / 1000) + 55 } });
    Object.assign(base.home, { linked_phone: "", last_sent_at: null, recipients: 0, agents_total: 0, agents_connected: 0 });
  }
  if (scenario === "expired") {
    Object.assign(base, { state: "unlinked", has_qr: false, pairing: { active: false, expired: true, started: true, code_expires_at: 0 } });
    Object.assign(base.home, { linked_phone: "", recipients: 1, agents_total: 0, agents_connected: 0 });
  }
  if (scenario === "outdated") {
    base.state = "outdated";
  }
  if (scenario === "paused") {
    Object.assign(base.home, { paused: true, pause_until: new Date(Date.now() + 40 * 60000).toISOString() });
  }
  const msgs = messages(busy ? 60 : 12);
  return {
    state: base,
    messages: scenario === "fresh" ? [] : msgs,
    recipients: Array.from({ length: busy ? 5 : 1 }, (_, i) => ({ id: `r${i}`, name: i ? `Teammate ${i}` : "Admin", phone_e164: `+2010000000${String(i).padStart(2, "0")}`, jid: "", enabled: i !== 2, levels: i % 2 ? 7 : 5 })),
    sessions: sessions(busy ? 8 : 2),
    keys: Array.from({ length: busy ? 6 : 2 }, (_, i) => ({ id: `k${i}`, label: `Work key ${i}`, masked: "test...6789", added_at: ago(60 * 24 * i) })),
    heartbeats: Array.from({ length: busy ? 5 : 2 }, (_, i) => ({ id: `h${i}`, label: `Plan ${i}`, source: "codex", session: "", interval_s: 600, grace_s: 300, last_beat_at: ago(i), state: i === 1 ? "alerted" : "active" })),
  };
}

const EVENTS = {
  calm: ["task_start", "task_end", "failure", "blocked", "critical_stop"],
  normal: ["task_start", "task_end", "failure", "blocked", "critical_stop", "phase_finished", "implementer_stopped"],
  detailed: ["task_start", "task_end", "failure", "blocked", "critical_stop", "phase_finished", "implementer_stopped", "step_milestone"],
};
const LEVEL_OF = { task_start: "info", task_end: "info", failure: "warning", blocked: "warning", critical_stop: "critical", phase_finished: "info", implementer_stopped: "warning", step_milestone: "info" };

const config = { scenario: "ok", language: "en" };
const savedOptions = {
  agent: "codex",
  other_file: "",
  scope: "global",
  template: "normal",
  levels: {},
  modes: {},
  heartbeat: true,
  heartbeat_every_s: 600,
  language: "egyptian",
  activation: "on_request",
  activate_phrases: ["notify on", "start alerts"],
  deactivate_phrases: ["notify off", "stop alerts"],
  extra: "",
};

function json(res, status, body) {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(JSON.stringify(body));
}

function api(req, res, url, body) {
  const data = build(config.scenario);
  const route = url.pathname.replace("/api/v1/ui", "");
  const method = req.method;
  if (route === "/session") return json(res, 200, { success: true, csrf_token: "stub" });
  if (route === "/state") return json(res, 200, { ...data.state, language: config.language });
  if (route === "/qr") {
    res.writeHead(200, { "Content-Type": "image/png" });
    const demoQr = path.join(here, "demo-qr.png");
    const file = config.scenario.startsWith("demo") && fs.existsSync(demoQr) ? demoQr : path.join(webRoot, "icons", "icon-green-256.png");
    return res.end(fs.readFileSync(file));
  }
  if (route === "/log") {
    const limit = Number(url.searchParams.get("limit")) || 100;
    return json(res, 200, data.messages.slice(0, limit));
  }
  if (route === "/recipients") return json(res, 200, data.recipients);
  if (route === "/rules") {
    return json(res, 200, {
      settings: { quiet_enabled: true, quiet_start: "22:00", quiet_end: "07:00", dedupe_minutes: 10, rate_limit_per_hour: 20, heartbeat_interval_s: 600, heartbeat_grace_s: 300 },
      pause: { paused: config.scenario === "paused", indefinite: false, until: new Date(Date.now() + 40 * 60000).toISOString() },
      autostart: { available: true, enabled: false },
      heartbeats: data.heartbeats,
    });
  }
  if (route === "/alerts") {
    return json(res, 200, {
      alerts: ["heartbeat_lost", "heartbeat_back", "connection_warning", "digest", "agent_connect"].map((kind, i) => ({ kind, enabled: kind !== "agent_connect", mode: i === 0 ? "both" : "text" })),
    });
  }
  if (route === "/voice") {
    return json(res, 200, {
      status: {
        key_count: data.keys.length,
        ffmpeg_found: true,
        ffmpeg_path: "C:\\ffmpeg\\bin\\ffmpeg.exe",
        ffmpeg_configured: "",
        available: true,
        voice: "Kore",
        voices: ["Kore", "Charon"],
        models: { primary: "gemini-3.8-flash-lite-tts", fallback: "gemini-3.8-flash-tts", custom: false },
        default_primary: "gemini-3.8-flash-lite-tts",
        default_fallback: "gemini-3.8-flash-tts",
        unavailable_models: {},
        modes: { info: "text", warning: "both", critical: "both" },
        speed: "normal",
        speeds: ["normal", "fast", "very_fast"],
      },
      keys: data.keys,
      spoken_intro: true,
    });
  }
  if (route === "/voice/models") {
    return json(res, 200, { models: [{ id: "gemini-3.8-flash-lite-tts", display_name: "Flash Lite TTS" }, { id: "gemini-3.8-flash-tts", display_name: "Flash TTS" }] });
  }
  if (route === "/agents") {
    return json(res, 200, {
      sessions: data.sessions,
      mute_all: false,
      notify_connect: false,
      skill: [
        { path: "C:\\Users\\dev\\.claude\\skills\\datacodex-notify", available: true, installed: true, up_to_date: true },
        { path: "C:\\Users\\dev\\.codex\\skills\\datacodex-notify", available: true, installed: true, up_to_date: false },
      ],
    });
  }
  if (route === "/agents/prompt-options") {
    if (method === "PUT") return json(res, 200, { success: true });
    const templates = {};
    for (const [name, events] of Object.entries(EVENTS)) {
      templates[name] = events.map((event) => ({ event, default_level: LEVEL_OF[event] }));
    }
    const modeDefaults = {};
    for (const name of Object.keys(EVENTS)) {
      modeDefaults[name] = { info: "text", warning: name === "detailed" ? "both" : "text", critical: "both" };
    }
    return json(res, 200, { options: savedOptions, defaults: savedOptions, templates, mode_defaults: modeDefaults, cli_path: "C:\\Notify\\cli.exe" });
  }
  if (route === "/agents/prompt") {
    return json(res, 200, { connect: "You are about to connect this agent to DataCodex Notify...\n".repeat(8), disconnect_generic: "Remove it." });
  }
  if (method === "PUT" || method === "POST" || method === "DELETE") return json(res, 200, { success: true });
  return json(res, 404, { error: "not found" });
}

export function startStub(port = 0) {
  const server = http.createServer((req, res) => {
    const url = new URL(req.url, "http://localhost");
    if (url.pathname === "/__cfg") {
      config.scenario = url.searchParams.get("scenario") || config.scenario;
      config.language = url.searchParams.get("lang") || config.language;
      return json(res, 200, config);
    }
    if (url.pathname.startsWith("/api/v1/ui/")) {
      const chunks = [];
      req.on("data", (c) => chunks.push(c));
      req.on("end", () => api(req, res, url, Buffer.concat(chunks).toString()));
      return undefined;
    }
    let file = url.pathname === "/" ? "/index.html" : url.pathname;
    file = path.join(webRoot, decodeURIComponent(file));
    if (!file.startsWith(webRoot) || !fs.existsSync(file) || fs.statSync(file).isDirectory()) {
      res.writeHead(404);
      return res.end();
    }
    res.writeHead(200, { "Content-Type": TYPES[path.extname(file)] || "application/octet-stream", "Cache-Control": "no-store" });
    return res.end(fs.readFileSync(file));
  });
  return new Promise((resolve) => server.listen(port, "127.0.0.1", () => resolve({ server, port: server.address().port })));
}

export { now };
