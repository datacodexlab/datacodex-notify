// Builds the documentation artwork: the WhatsApp-style chat mockups (texts come
// from the REAL template code through docs/assets/mockups/messages.json) and the
// architecture diagram, as HTML/SVG plus PNG renders. No external assets.
//
//   node tools/uicheck/docs-art.mjs
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { launch } from "./browser.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
const assets = path.join(root, "docs", "assets");
const messages = JSON.parse(fs.readFileSync(path.join(assets, "mockups", "messages.json"), "utf-8"));

const esc = (text) => text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
// WhatsApp style *bold* in the body, as the chat would render it.
const rich = (text) => esc(text).replace(/\*([^*\n]+)\*/g, "<b>$1</b>").replace(/\n/g, "<br>");

const L = {
  en: { dir: "ltr", lang: "en", voice: "Voice note", time: "10:42", chat: "Admin", sub: "DataCodex Notify" },
  ar: { dir: "rtl", lang: "ar", voice: "رسالة صوتية", time: "10:42", chat: "المسؤول", sub: "DataCodex Notify" },
};

function waveform() {
  // A fixed, harmless shape: the bars are decoration, not real audio.
  const heights = [6, 10, 16, 8, 20, 14, 6, 18, 24, 12, 8, 20, 16, 6, 12, 22, 10, 14, 8, 18, 12, 6, 16, 10, 20, 8, 14, 6];
  return heights.map((h) => `<i style="height:${h}px"></i>`).join("");
}

function chatHtml(lang) {
  const t = L[lang];
  const items = messages[lang];
  const by = Object.fromEntries(items.map((i) => [i.kind, i.text]));
  const bubble = (text) => `<div class="bubble">${rich(text)}</div>`;
  const voice = `<div class="bubble voice"><div class="play"></div><div class="wave">${waveform()}</div><span class="dur">0:07</span><div class="said">${esc(by["voice-speech"])}</div></div>`;
  return `<!doctype html>
<html lang="${t.lang}" dir="${t.dir}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Chat mockup</title>
<style>
:root{--bg:#e8efe9;--bar:#1f6f5c;--bubble:#ffffff;--text:#14171c;--muted:#6b7480;--accent:#2f6bff}
*{box-sizing:border-box}
body{margin:0;background:#cfd8d2;font:15px/1.5 "IBM Plex Sans Arabic","Segoe UI",system-ui,sans-serif;color:var(--text)}
.phone{width:440px;margin:0 auto;background:var(--bg);min-height:100vh}
.top{background:var(--bar);color:#fff;padding:12px 16px;display:flex;align-items:center;gap:12px}
.avatar{width:38px;height:38px;border-radius:50%;background:#ffffff33;display:flex;align-items:center;justify-content:center;font-weight:600}
.top b{display:block;font-size:16px}.top span{font-size:12px;opacity:.8}
.chat{padding:14px 12px 18px;display:flex;flex-direction:column;gap:10px}
.day{align-self:center;background:#ffffffb0;border-radius:8px;padding:2px 10px;font-size:12px;color:var(--muted)}
.bubble{background:var(--bubble);border-radius:12px;padding:9px 12px 7px;max-width:92%;align-self:flex-start;box-shadow:0 1px 1px #0000001a;word-break:break-word;white-space:normal;font-size:14.5px}
.bubble b{font-weight:700}
.voice{display:flex;flex-wrap:wrap;align-items:center;gap:10px;width:82%}
.play{width:0;height:0;border-inline-start:14px solid var(--bar);border-block:9px solid transparent;margin-inline:6px 2px}
.wave{display:flex;align-items:center;gap:2px;flex:1;height:28px}
.wave i{display:block;width:3px;background:var(--bar);border-radius:2px;opacity:.85}
.dur{font-size:12px;color:var(--muted)}
.said{flex-basis:100%;font-size:12px;color:var(--muted);border-top:1px solid #0000000f;padding-top:5px}
</style></head><body><div class="phone">
<div class="top"><div class="avatar">D</div><div><b>${esc(t.chat)}</b><span>${esc(t.sub)}</span></div></div>
<div class="chat">
${bubble(by["codex-info"])}
${bubble(by["claude-warning"])}
${voice}
${bubble(by["codex-critical"])}
${bubble(by["heartbeat-lost"])}
${bubble(by["digest"])}
</div></body></html>`;
}

const STR = {
  en: {
    agent: ["AI agent", "Claude, Codex, more"],
    cli: ["CLI", "datacodex-notify-cli"],
    app: ["Local app", "127.0.0.1 only"],
    lib: ["whatsmeow", "Go client library"],
    wa: ["WhatsApp", "linked device"],
    phone: ["Your phone", "text + voice"],
    tts: ["Gemini TTS + ffmpeg", "voice notes (optional)"],
    note: "Everything stays on your PC until the message leaves for WhatsApp.",
  },
  ar: {
    agent: ["وكيل ذكاء اصطناعي", "Claude, Codex, ..."],
    cli: ["أداة الأوامر", "datacodex-notify-cli"],
    app: ["التطبيق المحلي", "127.0.0.1 فقط"],
    lib: ["whatsmeow", "بروتوكول واتساب ويب"],
    wa: ["واتساب", "جهاز مرتبط"],
    phone: ["هاتفك", "نص + صوت"],
    tts: ["Gemini TTS + ffmpeg", "رسائل صوتية (اختياري)"],
    note: "كل شيء يبقى على جهازك حتى تغادر الرسالة إلى واتساب.",
  },
};

function diagramSvg(lang) {
  const s = STR[lang];
  const rtl = lang === "ar";
  const W = 960;
  const nodeW = 130;
  const gap = 26;
  const y = 70;
  const h = 70;
  const order = ["agent", "cli", "app", "lib", "wa", "phone"];
  const x = (i) => (rtl ? W - 20 - nodeW - i * (nodeW + gap) : 20 + i * (nodeW + gap));
  const nodes = order
    .map((key, i) => {
      const cls = key === "app" ? "node hot" : "node";
      return `<g><rect class="${cls}" x="${x(i)}" y="${y}" width="${nodeW}" height="${h}" rx="12"/><text class="t1" x="${x(i) + nodeW / 2}" y="${y + 30}" text-anchor="middle">${esc(s[key][0])}</text><text class="t2" x="${x(i) + nodeW / 2}" y="${y + 52}" text-anchor="middle">${esc(s[key][1])}</text></g>`;
    })
    .join("");
  const arrows = order
    .slice(0, -1)
    .map((_, i) => {
      const from = rtl ? x(i) : x(i) + nodeW;
      const to = rtl ? x(i + 1) + nodeW : x(i + 1);
      return `<line class="arrow" x1="${from}" y1="${y + h / 2}" x2="${to + (rtl ? 6 : -6)}" y2="${y + h / 2}" marker-end="url(#head)"/>`;
    })
    .join("");
  const appX = x(2);
  const ttsX = appX - 20;
  const ttsY = 210;
  const side = `<g><rect class="node side" x="${ttsX}" y="${ttsY}" width="${nodeW + 40}" height="64" rx="12"/><text class="t1" x="${ttsX + (nodeW + 40) / 2}" y="${ttsY + 28}" text-anchor="middle">${esc(s.tts[0])}</text><text class="t2" x="${ttsX + (nodeW + 40) / 2}" y="${ttsY + 48}" text-anchor="middle">${esc(s.tts[1])}</text></g><line class="arrow dashed" x1="${appX + nodeW / 2}" y1="${y + h}" x2="${appX + nodeW / 2}" y2="${ttsY - 6}" marker-end="url(#head)"/>`;
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${W} 330" role="img" aria-label="${esc(s.note)}" direction="${rtl ? "rtl" : "ltr"}">
<style>
.node{fill:#ffffff;stroke:#c9d0da;stroke-width:1.5}
.node.hot{fill:#eaf0ff;stroke:#2f6bff;stroke-width:2.5}
.node.side{fill:#fff7e6;stroke:#c98a00;stroke-dasharray:5 4}
.t1{font:600 14px "IBM Plex Sans Arabic","Segoe UI",system-ui,sans-serif;fill:#14171c}
.t2{font:12px "IBM Plex Sans Arabic","Segoe UI",system-ui,sans-serif;fill:#5b6472}
.arrow{stroke:#2f6bff;stroke-width:2.2}.arrow.dashed{stroke:#c98a00;stroke-dasharray:5 4}
#head path{fill:#2f6bff}
.note{font:13px "IBM Plex Sans Arabic","Segoe UI",system-ui,sans-serif;fill:#5b6472}
@media (prefers-color-scheme:dark){.node{fill:#171b21;stroke:#3a4250}.node.hot{fill:#1d2a4a;stroke:#5a8bff}.node.side{fill:#2a2412;stroke:#e3a82b}.t1{fill:#e8ebf0}.t2,.note{fill:#9aa3b2}.arrow{stroke:#5a8bff}#head path{fill:#5a8bff}}
</style>
<defs><marker id="head" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0 0L10 5L0 10z"/></marker></defs>
${arrows}${nodes}${side}
<text class="note" x="${W / 2}" y="318" text-anchor="middle">${esc(s.note)}</text>
</svg>`;
}

fs.mkdirSync(path.join(assets, "mockups"), { recursive: true });
fs.mkdirSync(path.join(assets, "diagrams"), { recursive: true });
for (const lang of ["en", "ar"]) {
  fs.writeFileSync(path.join(assets, "mockups", `chat-${lang}.html`), chatHtml(lang));
  fs.writeFileSync(path.join(assets, "diagrams", `architecture-${lang}.svg`), diagramSvg(lang));
}

const browser = await launch();
if (!browser) {
  console.log("SKIP: Microsoft Edge was not found; wrote the HTML and SVG only");
  process.exit(0);
}
await browser.viewport(440, 900, 2);
for (const lang of ["en", "ar"]) {
  await browser.open(pathToFileURL(path.join(assets, "mockups", `chat-${lang}.html`)).href, 600);
  const height = await browser.cdp.eval("document.querySelector('.phone').getBoundingClientRect().height");
  await browser.viewport(440, Math.ceil(height), 2);
  await browser.open(pathToFileURL(path.join(assets, "mockups", `chat-${lang}.html`)).href, 400);
  await browser.shot(path.join(assets, "mockups", `chat-${lang}.png`));
  await browser.viewport(960, 330, 2);
  const svg = fs.readFileSync(path.join(assets, "diagrams", `architecture-${lang}.svg`), "utf-8");
  const page = `<!doctype html><html style="background:#fff"><body style="margin:0;background:#fff;overflow:hidden"><div style="display:block;line-height:0">${svg.replace("<svg ", '<svg width="960" height="330" style="display:block" ')}</div></body></html>`;
  const tmp = path.join(assets, "diagrams", `.render-${lang}.html`);
  fs.writeFileSync(tmp, page);
  await browser.open(pathToFileURL(tmp).href, 500);
  await browser.shot(path.join(assets, "diagrams", `architecture-${lang}.png`));
  fs.rmSync(tmp);
  await browser.viewport(440, 900, 2);
}
await browser.close();
console.log("mockups and diagrams written");
