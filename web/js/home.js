import { api, qrUrl } from "./api.js";
import { el, icon, toast } from "./dom.js";
import { language, t } from "./i18n.js";
import { createLogPanel, messageLine } from "./log.js";

const DOT_CLASS = {
  connected: "dot-green",
  connecting: "dot-yellow",
  reconnecting: "dot-yellow",
};

export function dotClass(state) {
  return DOT_CLASS[state] || "dot-red";
}

const RECENT_COUNT = 3;
const UNLINKED = new Set(["unlinked", "logged_out"]);

function agoText(iso) {
  if (!iso) {
    return t("home.no_messages");
  }
  const minutes = Math.max(0, Math.floor((Date.now() - new Date(iso).getTime()) / 60000));
  if (minutes < 1) {
    return t("home.ago_now");
  }
  if (minutes < 60) {
    return t("home.ago_min", { n: String(minutes) });
  }
  if (minutes < 60 * 24) {
    return t("home.ago_hour", { n: String(Math.floor(minutes / 60)) });
  }
  return t("home.ago_day", { n: String(Math.floor(minutes / (60 * 24))) });
}

/**
 * The Home tab: connection status (or the pairing code), agents and pause,
 * recent messages with the full log one click away, test messages, and the
 * first-run checklist.
 */
export function createHomeView(root, { onChanged, navigate }) {
  let lastState = null;
  let requesting = false;
  let autoTried = false;
  let expiresAt = 0;
  let recent = [];

  // ---- pairing block (replaces the status card while not linked)
  const qr = el("img", { class: "qr", alt: "" });
  const refresh = el(
    "button",
    { class: "btn btn-primary qr-refresh", type: "button", onclick: () => requestPairing() },
    el("span", { class: "refresh-icon", "aria-hidden": "true" }),
    el("span", { class: "refresh-label" }),
  );
  refresh.querySelector(".refresh-icon").innerHTML =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-3-6.7"/><path d="M21 3v6h-6"/></svg>';
  const spinner = el("div", { class: "spinner", role: "status" });
  const qrBox = el("div", { class: "qr-box" }, qr, refresh, spinner);
  const pairTitle = el("h2", {});
  const countdown = el("p", { class: "muted center" });
  const pairNote = el("p", { class: "muted center" });
  const steps = el("ol", { class: "steps" });
  const pairCard = el("div", { class: "card pair-card" }, pairTitle, qrBox, countdown, pairNote, steps);
  const banner = el("div", { class: "card banner-error", role: "alert" });

  // ---- status card
  const dot = el("span", { class: "dot dot-red", "aria-hidden": "true" });
  const main = el("div", { class: "state-main" });
  const phone = el("div", { class: "muted" });
  const last = el("div", { class: "muted" });
  const statusCard = el("div", { class: "card state-card" }, dot, el("div", { class: "row-main" }, main, phone, last));

  // ---- agents and pause
  const agentsChip = el("button", { class: "chip-button", type: "button", onclick: () => navigate("agents", "connected") });
  const pauseButton = el("button", { class: "btn btn-small", type: "button", onclick: onPause });
  const controlsCard = el("div", { class: "card controls-card" }, agentsChip, pauseButton);

  // ---- recent messages
  const recentTitle = el("h2", {});
  const viewAll = el("button", { class: "btn btn-small btn-ghost", type: "button", onclick: () => showLog(true) });
  const recentList = el("div", { class: "recent-list" });
  const recentCard = el("div", { class: "card recent-card" }, el("div", { class: "card-head" }, recentTitle, viewAll), recentList);

  // ---- tests
  const testText = el("button", { class: "btn btn-small", type: "button", onclick: () => sendTest("text") });
  const testVoice = el("button", { class: "btn btn-small", type: "button", onclick: () => sendTest("voice") });
  const testBoth = el("button", { class: "btn btn-small btn-ghost", type: "button", onclick: () => sendTest("both") });
  const moreBox = el("details", { class: "more-menu" }, el("summary", { class: "btn btn-small btn-ghost" }), el("div", { class: "more-pop" }, testBoth));
  const testsRow = el("div", { class: "tests-row" }, testText, testVoice, moreBox);

  // ---- first-run checklist
  const checkItems = [
    { key: "link", go: () => showLog(false) },
    { key: "number", go: () => navigate("settings", "recipients") },
    { key: "agent", go: () => navigate("agents", "new") },
  ].map((item) => ({ ...item, node: el("button", { class: "check-item", type: "button", onclick: item.go }) }));
  const checkTitle = el("div", { class: "check-title" });
  const checkHint = el("div", { class: "muted" });
  const checklist = el("div", { class: "card checklist" }, checkTitle, el("div", { class: "check-row" }, ...checkItems.map((i) => i.node)), checkHint);

  // ---- full log (inside Home)
  const logPanel = createLogPanel();
  const back = el("button", { class: "btn btn-small btn-ghost", type: "button", onclick: () => showLog(false) });
  const logTitle = el("h2", {});
  const logCard = el("div", { class: "card log-card" }, el("div", { class: "card-head" }, back, logTitle), logPanel.element);

  const mainBlock = el("div", { class: "home-main" }, banner, pairCard, statusCard, controlsCard, recentCard, testsRow, checklist);
  root.append(mainBlock, logCard);
  logCard.hidden = true;

  function showLog(open) {
    logCard.hidden = !open;
    mainBlock.hidden = open;
    if (open) {
      logPanel.show();
    } else {
      loadRecent();
    }
  }

  async function loadRecent() {
    try {
      recent = await api("GET", `/log?limit=${RECENT_COUNT}`);
      renderRecent();
    } catch {
      // The next poll tries again; the rest of Home keeps working.
    }
  }

  function renderRecent() {
    recentList.replaceChildren(
      ...(recent.length ? recent.map((m) => el("div", { class: "row" }, messageLine(m))) : [el("div", { class: "empty" }, t("home.no_messages"))]),
    );
  }

  async function requestPairing() {
    if (requesting) {
      return;
    }
    requesting = true;
    refresh.disabled = true;
    spinner.hidden = false;
    refresh.hidden = true;
    try {
      await api("POST", "/pair");
    } catch (err) {
      toast(err.message, true);
    } finally {
      requesting = false;
      refresh.disabled = false;
    }
  }

  async function sendTest(mode) {
    moreBox.open = false;
    try {
      await api("POST", "/test", { mode });
      toast(t("actions.test_queued"));
      onChanged();
    } catch (err) {
      toast(err.message, true);
    }
  }

  async function onPause() {
    const paused = lastState && lastState.home && lastState.home.paused;
    try {
      if (paused) {
        await api("DELETE", "/pause");
        toast(t("rules.resumed_done"));
      } else {
        await api("POST", "/pause", { minutes: 60 });
        toast(t("rules.paused_done"));
      }
      onChanged();
    } catch (err) {
      toast(err.message, true);
    }
  }

  function tickCountdown() {
    if (!expiresAt) {
      countdown.textContent = "";
      return;
    }
    const left = Math.max(0, expiresAt - Math.floor(Date.now() / 1000));
    countdown.textContent = `${t("connection.expires_in")} ${left}s`;
  }
  setInterval(tickCountdown, 1000);

  function renderStatic() {
    pairTitle.textContent = t("connection.qr_title");
    steps.replaceChildren(...["step1", "step2", "step3"].map((key) => el("li", {}, t(`connection.${key}`))));
    refresh.querySelector(".refresh-label").textContent = t("connection.show_new_code");
    banner.textContent = t("connection.outdated_banner");
    recentTitle.textContent = t("home.recent");
    viewAll.textContent = t("home.view_all");
    back.replaceChildren(icon("back"), t("common.back"));
    logTitle.textContent = t("home.all_messages");
    testText.textContent = t("home.test_text");
    testVoice.textContent = t("home.test_voice");
    testBoth.textContent = t("connection.test_both");
    moreBox.querySelector("summary").textContent = t("home.more");
    checkTitle.textContent = t("home.check_title");
    checkHint.textContent = t("home.check_hint");
  }

  function renderChecklist(state) {
    const home = state.home || {};
    const linked = !UNLINKED.has(state.state) && state.state !== "outdated" && (state.state === "connected" || state.state === "reconnecting" || Boolean(home.linked_phone));
    const done = { link: linked, number: (home.recipients || 0) > 0, agent: (home.agents_total || 0) > 0 };
    for (const item of checkItems) {
      const complete = done[item.key];
      item.node.className = `check-item${complete ? " done" : ""}`;
      item.node.replaceChildren(el("span", { class: "check-mark" }, complete ? "\u2713" : String(checkItems.indexOf(item) + 1)), el("span", {}, t(`home.check_${item.key}`)));
    }
    checklist.hidden = Object.values(done).every(Boolean);
  }

  return {
    update(state) {
      lastState = state;
      renderStatic();
      const home = state.home || {};
      const needsLink = UNLINKED.has(state.state);
      const outdated = state.state === "outdated";
      const connecting = state.state === "connecting";

      banner.hidden = !outdated;
      pairCard.hidden = !needsLink;
      statusCard.hidden = needsLink || outdated;
      controlsCard.hidden = needsLink || outdated;
      recentCard.hidden = needsLink || outdated;
      testsRow.hidden = needsLink || outdated || connecting;

      dot.className = `dot ${dotClass(state.state)}`;
      main.textContent = t(`state.${state.state}`);
      const [before, after] = t("home.linked_as", { phone: "" }).split("");
      phone.replaceChildren(before, el("bdi", { class: "number" }, home.linked_phone || ""), after || "");
      phone.hidden = !home.linked_phone;
      last.textContent = `${t("home.last_message")}: ${agoText(home.last_sent_at)}`;

      agentsChip.textContent = t("home.agents_active", { n: String(home.agents_connected || 0) });
      pauseButton.textContent = home.paused ? t("home.resume") : t("home.pause_1h");
      pauseButton.classList.toggle("btn-primary", Boolean(home.paused));

      // Pairing: the first look at an unlinked Home asks for one code; later codes need a click.
      const pairing = state.pairing || {};
      if (needsLink && !pairing.started && !pairing.active && !autoTried) {
        autoTried = true;
        requestPairing();
      }
      const starting = requesting || (pairing.active && !state.has_qr);
      if (state.has_qr || !starting) {
        requesting = false;
      }
      qr.hidden = !state.has_qr;
      spinner.hidden = state.has_qr || !starting;
      refresh.hidden = state.has_qr || starting;
      pairNote.hidden = true;
      expiresAt = state.has_qr ? pairing.code_expires_at || 0 : 0;
      tickCountdown();
      if (!state.has_qr && !starting && pairing.expired) {
        pairNote.textContent = t("connection.code_expired");
        pairNote.hidden = false;
      }
      if (state.has_qr) {
        qr.src = qrUrl();
      }

      renderChecklist(state);
      if (logCard.hidden) {
        loadRecent();
      } else {
        logPanel.refresh();
      }
    },
    show() {
      showLog(false);
    },
    refreshLanguage() {
      renderStatic();
      renderRecent();
      logPanel.refreshLanguage();
      if (lastState) {
        this.update(lastState);
      }
    },
  };
}

export { language };
