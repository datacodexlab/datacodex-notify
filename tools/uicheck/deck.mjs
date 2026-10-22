// Checks docs/presentation/index.html from file:// in both languages and both
// modes (landing page and presentation mode), at desktop and phone sizes: it must
// load without script errors or broken images, never overflow sideways, and every
// slide must fit its page.
//   node tools/uicheck/deck.mjs [outDir]
import path from "node:path";
import { pathToFileURL, fileURLToPath } from "node:url";
import { launch, sleep } from "./browser.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
const outDir = process.argv[2] ? path.resolve(process.argv[2]) : null;
const page = pathToFileURL(path.join(root, "docs", "presentation", "index.html")).href;
const SECTIONS = ["problem", "solution", "how", "install", "connect", "phone", "rules", "voice", "privacy", "start"];

const browser = await launch();
if (!browser) {
  console.log("SKIP: Microsoft Edge was not found");
  process.exit(0);
}
const failures = [];
const errors = [];
browser.cdp.ws.addEventListener("message", (event) => {
  const msg = JSON.parse(event.data);
  if (msg.method === "Runtime.exceptionThrown") errors.push(msg.params.exceptionDetails.exception?.description || msg.params.exceptionDetails.text);
});

const blank = async () => {
  await browser.cdp.send("Page.navigate", { url: "about:blank" });
  await sleep(150);
};
const broken = `[...document.images].filter(i => i.getAttribute('src') && (!i.complete || i.naturalWidth === 0)).map(i => i.getAttribute('src'))`;

for (const [label, w, h] of [["desktop", 1280, 720], ["phone", 390, 800]]) {
  await browser.viewport(w, h, 1);
  for (const lang of ["en", "ar"]) {
    // ---- landing mode
    await blank();
    await browser.open(`${page}?lang=${lang}`, 700);
    const info = await browser.cdp.eval(`(async () => {
      for (const s of document.querySelectorAll('.sec')) { s.scrollIntoView(); await new Promise(r => setTimeout(r, 120)); }
      window.scrollTo({ top: 0, behavior: 'instant' }); await new Promise(r => setTimeout(r, 300));
      const ids = [...document.querySelectorAll('.sec')].map(s => s.id);
      const anchors = [...document.querySelectorAll('#links a')].map(a => a.getAttribute('href').slice(1));
      return { sections: ids, missing: anchors.filter(a => a !== 'start' && !ids.includes(a)), broken: ${broken}, wide: document.documentElement.scrollWidth - document.documentElement.clientWidth, hidden: [...document.querySelectorAll('.reveal')].filter(n => getComputedStyle(n).opacity === '0').length, dir: document.documentElement.dir };
    })()`);
    const tag = `${label} ${lang} landing`;
    if (JSON.stringify(info.sections) !== JSON.stringify(SECTIONS)) failures.push(`${tag}: sections are ${info.sections}`);
    if (info.missing.length) failures.push(`${tag}: nav anchors without a section: ${info.missing}`);
    if (info.broken.length) failures.push(`${tag}: broken images ${info.broken}`);
    if (info.wide > 2) failures.push(`${tag}: page is ${info.wide}px wider than the screen`);
    if (info.dir !== (lang === "ar" ? "rtl" : "ltr")) failures.push(`${tag}: wrong direction ${info.dir}`);
    if (outDir) await browser.shot(path.join(outDir, `${label}-${lang}-landing-top.png`));

    // ---- presentation mode
    for (let n = 1; n <= SECTIONS.length; n++) {
      await blank();
      await browser.open(`${page}?lang=${lang}#slide-${n}`, 500);
      await sleep(350);
      const m = await browser.cdp.eval(`(() => { const s = document.querySelector('.sec.active'); return { deck: document.body.classList.contains('deck-mode'), id: s && s.id, over: s ? s.scrollHeight - s.clientHeight : -1, wide: s ? s.scrollWidth - s.clientWidth : -1, broken: ${broken} } })()`);
      const stag = `${label} ${lang} slide ${n}`;
      if (!m.deck || m.id !== SECTIONS[n - 1]) failures.push(`${stag}: presentation mode did not open the right slide (${m.id})`);
      if (m.broken.length) failures.push(`${stag}: broken images ${m.broken.join(",")}`);
      if (w > 700 && m.over > 2) failures.push(`${stag}: slide scrolls by ${m.over}px`);
      if (m.wide > 2) failures.push(`${stag}: slide is wider by ${m.wide}px`);
      if (outDir && n % 3 === 0) await browser.shot(path.join(outDir, `${label}-${lang}-slide-${String(n).padStart(2, "0")}.png`));
    }

    // ---- Esc leaves presentation mode and returns to the same section
    await blank();
    await browser.open(`${page}?lang=${lang}#slide-5`, 500);
    await browser.cdp.eval(`document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))`);
    await sleep(300);
    const left = await browser.cdp.eval(`({ deck: document.body.classList.contains('deck-mode'), hash: location.hash })`);
    if (left.deck || left.hash !== "#connect") failures.push(`${label} ${lang}: Esc did not return to the landing page (${JSON.stringify(left)})`);
  }
}
await browser.close();
if (errors.length) failures.push(`script errors: ${errors.slice(0, 3).join(" | ")}`);
console.log(failures.length ? failures.join(String.fromCharCode(10)) : "landing page and presentation mode load and fit (both languages, desktop and phone)");
process.exit(failures.length ? 1 : 0);
