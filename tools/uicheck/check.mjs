// Renders every tab and sub-tab of the admin UI in headless Edge, in both
// languages, at the app window size, and fails when a page needs scrolling.
//
//   node tools/uicheck/check.mjs [outDir] [width] [height]
//
// Screenshots are written to outDir. Exit code 1 means some page overflows.
import { spawn } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { startStub } from "./stub.mjs";

const outDir = path.resolve(process.argv[2] || path.join(os.tmpdir(), "uicheck"));
const width = Number(process.argv[3]) || 460;
const height = Number(process.argv[4]) || 680;

function findEdge() {
  const candidates = [
    "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
    "C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe",
  ];
  return candidates.find((p) => fs.existsSync(p));
}

const edge = findEdge();
if (!edge) {
  console.log("SKIP: Microsoft Edge was not found");
  process.exit(0);
}
fs.mkdirSync(outDir, { recursive: true });

const tab = (name) => `document.querySelector('.tab[data-tab="${name}"]').click()`;
const sub = (tabName, key) => `${tab(tabName)}; document.querySelector('#view-${tabName} [data-sub="${key}"]').click()`;
const click = (selector) => `document.querySelector(${JSON.stringify(selector)}).click()`;
const open = (selector) => `document.querySelector(${JSON.stringify(selector)}).open = true`;

// Each case: name, stub scenario, and the page actions that bring the view up.
const cases = [
  { name: "home-ok", scenario: "ok", actions: [] },
  { name: "home-busy", scenario: "busy", actions: [] },
  { name: "home-paused", scenario: "paused", actions: [] },
  { name: "home-more-menu", scenario: "ok", actions: [open(".more-menu")] },
  { name: "home-log", scenario: "busy", actions: [click(".home-main .recent-card .btn-ghost")] },
  { name: "home-log-expanded", scenario: "busy", actions: [click(".home-main .recent-card .btn-ghost"), click(".log-row")] },
  { name: "home-unlinked-qr", scenario: "fresh", actions: [] },
  { name: "home-code-expired", scenario: "expired", actions: [] },
  { name: "home-outdated", scenario: "outdated", actions: [] },
  { name: "agents-new-1", scenario: "ok", actions: [sub("agents", "new")] },
  { name: "agents-new-2", scenario: "ok", actions: [sub("agents", "new"), click(".wizard-nav .btn-primary")] },
  { name: "agents-new-2-customize", scenario: "ok", actions: [sub("agents", "new"), click(".wizard-nav .btn-primary"), open(".wizard-card details.expander")] },
  { name: "agents-new-3", scenario: "ok", actions: [sub("agents", "new"), click(".wizard-nav .btn-primary"), click(".wizard-nav .btn-primary")] },
  { name: "agents-new-3-prompt", scenario: "ok", actions: [sub("agents", "new"), click(".wizard-nav .btn-primary"), click(".wizard-nav .btn-primary"), click(".wizard-body:not([hidden]) > .btn-ghost")] },
  { name: "agents-new-3-more", scenario: "ok", actions: [sub("agents", "new"), click(".wizard-nav .btn-primary"), click(".wizard-nav .btn-primary"), open(".wizard-body:not([hidden]) details.expander")] },
  { name: "agents-connected", scenario: "busy", actions: [sub("agents", "connected")] },
  { name: "agents-advanced", scenario: "ok", actions: [sub("agents", "advanced")] },
  { name: "alerts-quiet", scenario: "paused", actions: [sub("alerts", "quiet")] },
  { name: "alerts-app", scenario: "ok", actions: [sub("alerts", "alerts")] },
  { name: "alerts-heartbeat", scenario: "busy", actions: [sub("alerts", "heartbeat")] },
  { name: "voice-keys", scenario: "busy", actions: [sub("voice", "keys")] },
  { name: "voice-voice", scenario: "ok", actions: [sub("voice", "voice")] },
  { name: "voice-models", scenario: "ok", actions: [sub("voice", "models")] },
  { name: "voice-models-picker", scenario: "ok", actions: [sub("voice", "models"), click("#view-voice .subpanel:not([hidden]) .form-actions .btn")] },
  { name: "settings-recipients", scenario: "busy", actions: [sub("settings", "recipients")] },
  { name: "settings-general", scenario: "ok", actions: [sub("settings", "general")] },
  { name: "settings-general-advanced", scenario: "ok", actions: [sub("settings", "general"), open("#view-settings details.expander")] },
];

const measure = `(() => {
  const content = document.querySelector('.content');
  const wide = [];
  for (const node of document.querySelectorAll('.app *')) {
    const r = node.getBoundingClientRect();
    if (r.width === 0 || node.closest('[hidden]') || node.closest('.expander-body') || node.closest('.pager-list')) continue;
    if (r.right > window.innerWidth + 1 || r.left < -1) wide.push(node.tagName.toLowerCase() + '.' + String(node.className).split(' ')[0]);
  }
  return {
    docH: document.documentElement.scrollHeight,
    vh: window.innerHeight,
    contentOver: content.scrollHeight - content.clientHeight,
    docW: document.documentElement.scrollWidth,
    vw: window.innerWidth,
    wide: wide.slice(0, 5),
  };
})()`;

class Cdp {
  constructor(ws) {
    this.ws = ws;
    this.id = 0;
    this.pending = new Map();
    this.waiters = [];
    this.errors = [];
    ws.addEventListener("message", (event) => {
      const msg = JSON.parse(event.data);
      if (msg.id && this.pending.has(msg.id)) {
        const { resolve, reject } = this.pending.get(msg.id);
        this.pending.delete(msg.id);
        msg.error ? reject(new Error(msg.error.message)) : resolve(msg.result);
      } else if (msg.method) {
        if (msg.method === "Runtime.exceptionThrown") {
          this.errors.push(msg.params.exceptionDetails.exception?.description || msg.params.exceptionDetails.text);
        }
        if (msg.method === "Runtime.consoleAPICalled" && msg.params.type === "error") {
          this.errors.push(msg.params.args.map((a) => a.value || a.description).join(" "));
        }
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

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function main() {
  const { server, port } = await startStub();
  const profile = fs.mkdtempSync(path.join(os.tmpdir(), "uicheck-edge-"));
  const debugPort = 9333 + Math.floor(Math.random() * 500);
  const child = spawn(edge, ["--headless=new", `--remote-debugging-port=${debugPort}`, `--user-data-dir=${profile}`, "--no-first-run", "--disable-gpu", "about:blank"], { stdio: "ignore" });

  let targets;
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
  await cdp.send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 1, mobile: false });

  const failures = [];
  const rows = [];
  for (const lang of ["en", "ar"]) {
    for (const c of cases) {
      await fetch(`http://127.0.0.1:${port}/__cfg?scenario=${c.scenario}&lang=${lang}`);
      const loaded = cdp.once("Page.loadEventFired");
      await cdp.send("Page.navigate", { url: `http://127.0.0.1:${port}/?launch=x` });
      await loaded;
      await sleep(900);
      for (const action of c.actions) {
        await cdp.eval(action);
        await sleep(450);
      }
      await sleep(300);
      const m = await cdp.eval(measure);
      const problems = [];
      if (cdp.errors.length) {
        problems.push(`script errors: ${cdp.errors.slice(0, 2).join(" | ")}`);
        cdp.errors = [];
      }
      if (m.docH > m.vh) problems.push(`page ${m.docH}>${m.vh}`);
      if (m.contentOver > 1) problems.push(`content scrolls by ${m.contentOver}px`);
      if (m.docW > m.vw) problems.push(`page wider ${m.docW}>${m.vw}`);
      if (m.wide.length) problems.push(`outside: ${m.wide.join(",")}`);
      const shot = await cdp.send("Page.captureScreenshot", { format: "png" });
      fs.writeFileSync(path.join(outDir, `${lang}-${c.name}.png`), Buffer.from(shot.data, "base64"));
      rows.push(`${problems.length ? "FAIL" : "ok  "} ${lang} ${c.name}${problems.length ? "  " + problems.join("; ") : ""}`);
      if (problems.length) failures.push(`${lang} ${c.name}`);
    }
  }

  console.log(rows.join("\n"));
  console.log(`\n${rows.length - failures.length}/${rows.length} pages fit ${width}x${height}. Screenshots: ${outDir}`);
  ws.close();
  child.kill();
  server.close();
  try {
    fs.rmSync(profile, { recursive: true, force: true });
  } catch {
    // The profile folder is temporary.
  }
  process.exit(failures.length ? 1 : 0);
}

main().catch((err) => {
  console.error(err);
  process.exit(2);
});
