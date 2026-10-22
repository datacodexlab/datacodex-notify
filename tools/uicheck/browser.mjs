// A tiny headless-Edge driver over the DevTools protocol, shared by the demo
// capture and the documentation renderers. It never touches real app data.
import { spawn } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

export function findEdge() {
  return [
    "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
    "C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe",
  ].find((p) => fs.existsSync(p));
}

class Cdp {
  constructor(ws) {
    this.ws = ws;
    this.id = 0;
    this.pending = new Map();
    this.waiters = [];
    ws.addEventListener("message", (event) => {
      const msg = JSON.parse(event.data);
      if (msg.id && this.pending.has(msg.id)) {
        const { resolve, reject } = this.pending.get(msg.id);
        this.pending.delete(msg.id);
        msg.error ? reject(new Error(msg.error.message)) : resolve(msg.result);
      } else if (msg.method) {
        this.waiters = this.waiters.filter((w) => (w.method === msg.method ? (w.resolve(msg.params), false) : true));
      }
    });
  }

  send(method, params = {}) {
    const id = ++this.id;
    this.ws.send(JSON.stringify({ id, method, params }));
    return new Promise((resolve, reject) => this.pending.set(id, { resolve, reject }));
  }

  once(method) {
    return new Promise((resolve) => this.waiters.push({ method, resolve }));
  }

  async eval(expression) {
    const result = await this.send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true });
    if (result.exceptionDetails) {
      throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text);
    }
    return result.result.value;
  }
}

/** Starts a throw-away headless Edge and returns a page driver, or null without Edge. */
export async function launch() {
  const edge = findEdge();
  if (!edge) {
    return null;
  }
  const profile = fs.mkdtempSync(path.join(os.tmpdir(), "dcn-browser-"));
  const debugPort = 9333 + Math.floor(Math.random() * 500);
  const child = spawn(edge, ["--headless=new", `--remote-debugging-port=${debugPort}`, `--user-data-dir=${profile}`, "--no-first-run", "--disable-gpu", "about:blank"], { stdio: "ignore" });
  let targets = [];
  for (let i = 0; i < 50; i++) {
    try {
      targets = await (await fetch(`http://127.0.0.1:${debugPort}/json`)).json();
      if (targets.some((t) => t.type === "page")) break;
    } catch {
      // Edge is still starting.
    }
    await sleep(200);
  }
  const page = targets.find((t) => t.type === "page");
  const ws = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((resolve) => ws.addEventListener("open", resolve));
  const cdp = new Cdp(ws);
  await cdp.send("Page.enable");
  await cdp.send("Runtime.enable");
  return {
    cdp,
    async viewport(width, height, scale = 1) {
      await cdp.send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: scale, mobile: false });
    },
    async open(url, settleMs = 900) {
      const loaded = cdp.once("Page.loadEventFired");
      await cdp.send("Page.navigate", { url });
      await loaded;
      await sleep(settleMs);
    },
    async shot(file, clip) {
      const params = { format: "png" };
      if (clip) params.clip = { ...clip, scale: 1 };
      const result = await cdp.send("Page.captureScreenshot", params);
      fs.mkdirSync(path.dirname(file), { recursive: true });
      fs.writeFileSync(file, Buffer.from(result.data, "base64"));
    },
    async close() {
      try {
        cdp.ws.close();
      } catch {
        // Already closed.
      }
      child.kill();
      await sleep(400);
      try {
        fs.rmSync(profile, { recursive: true, force: true });
      } catch {
        // Temporary folder.
      }
    },
  };
}
