// Captures the documentation screenshots from the stub API's DEMO data set
// (fake name, fake number, fake projects). It never reads the real data folder
// or Credential Manager. Output: <outDir>/<lang>/<name>.png at 460x680 (2x).
//
//   node tools/uicheck/demo.mjs [outDir]
import path from "node:path";
import { launch, sleep } from "./browser.mjs";
import { startStub } from "./stub.mjs";

const outDir = path.resolve(process.argv[2] || "docs/assets/screenshots");

const tab = (name) => `document.querySelector('.tab[data-tab="${name}"]').click()`;
const sub = (tabName, key) => `${tab(tabName)}; document.querySelector('#view-${tabName} [data-sub="${key}"]').click()`;
const click = (selector) => `document.querySelector(${JSON.stringify(selector)}).click()`;
const next = click(".wizard-nav .btn-primary");

const shots = [
  { name: "home-before-linking", scenario: "demo-fresh", actions: [] },
  { name: "home-linked", scenario: "demo", actions: [] },
  { name: "home-first-run", scenario: "demo-checklist", actions: [] },
  { name: "agents-step-1", scenario: "demo", actions: [sub("agents", "new")] },
  { name: "agents-step-2", scenario: "demo", actions: [sub("agents", "new"), next] },
  { name: "agents-step-3", scenario: "demo", actions: [sub("agents", "new"), next, next] },
  { name: "agents-connected", scenario: "demo", actions: [sub("agents", "connected")] },
  { name: "alerts-quiet-pause", scenario: "demo", actions: [sub("alerts", "quiet")] },
  { name: "alerts-app", scenario: "demo", actions: [sub("alerts", "alerts")] },
  { name: "voice-speed", scenario: "demo", actions: [sub("voice", "voice")] },
  { name: "voice-models", scenario: "demo", actions: [sub("voice", "models")] },
  { name: "settings-recipients", scenario: "demo", actions: [sub("settings", "recipients")] },
  { name: "settings-general", scenario: "demo", actions: [sub("settings", "general")] },
];

const browser = await launch();
if (!browser) {
  console.log("SKIP: Microsoft Edge was not found");
  process.exit(0);
}
const { server, port } = await startStub();
await browser.viewport(460, 680, 2);
for (const lang of ["en", "ar"]) {
  for (const shot of shots) {
    await fetch(`http://127.0.0.1:${port}/__cfg?scenario=${shot.scenario}&lang=${lang}`);
    await browser.open(`http://127.0.0.1:${port}/?launch=x`, 1100);
    for (const action of shot.actions) {
      await browser.cdp.eval(action);
      await sleep(450);
    }
    await sleep(300);
    await browser.shot(path.join(outDir, lang, `${shot.name}.png`));
    console.log(`${lang}/${shot.name}.png`);
  }
}
await browser.close();
server.close();
