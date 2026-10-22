import { api, startSession } from "./api.js";
import { toast } from "./dom.js";
import { createAgentsView } from "./agents.js";
import { createAlertsView } from "./alerts.js";
import { createHomeView, dotClass } from "./home.js";
import { language, setLanguage, t } from "./i18n.js";
import { createSettingsView } from "./settings.js";
import { createVoiceView } from "./voice.js";

const POLL_MS = 2000;
const ICON_BASE = "icons/icon-";

let activeTab = "home";
let lastState = null;
let languageLoaded = false;

const views = {
  home: createHomeView(document.getElementById("view-home"), { onChanged: () => poll(), navigate }),
  agents: createAgentsView(document.getElementById("view-agents")),
  alerts: createAlertsView(document.getElementById("view-alerts")),
  voice: createVoiceView(document.getElementById("view-voice")),
  settings: createSettingsView(document.getElementById("view-settings"), {
    onLanguage: changeLanguage,
    version: () => (lastState ? `v${lastState.app_version}` : ""),
  }),
};

function showTab(name, sub) {
  activeTab = name;
  for (const tab of document.querySelectorAll(".tab")) {
    const selected = tab.dataset.tab === name;
    tab.classList.toggle("active", selected);
    tab.setAttribute("aria-selected", String(selected));
  }
  for (const key of Object.keys(views)) {
    document.getElementById(`view-${key}`).hidden = key !== name;
  }
  if (sub && views[name].select) {
    views[name].select(sub);
  } else if (views[name].show) {
    views[name].show();
  }
}

function navigate(tab, sub) {
  showTab(tab, sub);
}

/** The favicon follows the connection state like the tray icon: green when connected, yellow while connecting, red otherwise. */
function setFavicon(state) {
  const color = { "dot-green": "green", "dot-yellow": "yellow" }[dotClass(state)] || "red";
  const link = document.getElementById("favicon");
  const href = `${ICON_BASE}${color}-32.png`;
  if (link.getAttribute("href") !== href) {
    link.setAttribute("href", href);
  }
}

function applyState(state) {
  lastState = state;
  document.getElementById("state-dot").className = `dot ${dotClass(state.state)}`;
  document.getElementById("state-label").textContent = t(`state.${state.state}`);
  setFavicon(state.state);
  views.home.update(state);
}

async function applyLanguage(lang) {
  await setLanguage(lang);
  for (const view of Object.values(views)) {
    if (view.refreshLanguage) {
      view.refreshLanguage();
    }
  }
  if (lastState) {
    applyState(lastState);
  }
}

async function changeLanguage(next) {
  try {
    await api("PUT", "/settings", { language: next });
    await applyLanguage(next);
  } catch (err) {
    toast(err.message, true);
  }
}

async function poll() {
  try {
    const state = await api("GET", "/state");
    if (!languageLoaded) {
      languageLoaded = true;
      await applyLanguage(state.language);
    }
    applyState(state);
    if (["agents", "alerts"].includes(activeTab) && views[activeTab].refresh) {
      views[activeTab].refresh();
    }
  } catch {
    showSessionProblem();
  }
}

function showSessionProblem() {
  const node = document.getElementById("view-session");
  node.textContent = t("session.expired");
  node.hidden = false;
  for (const key of Object.keys(views)) {
    document.getElementById(`view-${key}`).hidden = true;
  }
}

async function main() {
  await applyLanguage("ar");
  for (const tab of document.querySelectorAll(".tab")) {
    tab.addEventListener("click", () => showTab(tab.dataset.tab));
  }

  const ok = await startSession();
  if (!ok) {
    showSessionProblem();
    return;
  }
  await poll();
  setInterval(poll, POLL_MS);
}

main();

export { language };
