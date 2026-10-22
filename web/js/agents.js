import { api } from "./api.js";
import { copyText, createWizard } from "./agent-prompt.js";
import { el, icon, toast } from "./dom.js";
import { language, t } from "./i18n.js";
import { createPager, createSubtabs } from "./widgets.js";

const STALE_DISCONNECTED_MS = 60 * 60 * 1000;
const SESSIONS_PER_PAGE = 4;

function readFlag(key) {
  try {
    return localStorage.getItem(key) === "1";
  } catch {
    return false;
  }
}

function writeFlag(key, on) {
  try {
    localStorage.setItem(key, on ? "1" : "0");
  } catch {
    // The choice then lasts until the page reloads.
  }
}

function formatTime(iso) {
  if (!iso) {
    return t("agents.never");
  }
  return new Date(iso).toLocaleString(language() === "ar" ? "ar-EG" : "en-GB", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function toggleRow(id, onChange) {
  const box = el("input", { type: "checkbox", id });
  box.addEventListener("change", onChange);
  const title = el("span", { class: "row-title wrap" });
  const hint = el("span", { class: "muted" });
  const row = el("label", { class: "row switch-row", for: id }, el("span", { class: "row-main" }, title, hint), el("span", { class: "toggle" }, box, el("span", {})));
  return { box, title, hint, row };
}

/** Connected agents: a paginated list with mute, disconnect prompt, and remove. */
function createConnectedPanel(shared) {
  let data = null;
  let showDisconnected = readFlag("dcn_show_disconnected");

  const showDisc = el("input", { type: "checkbox", id: "agents-show-disconnected" });
  showDisc.checked = showDisconnected;
  showDisc.addEventListener("change", () => {
    showDisconnected = showDisc.checked;
    writeFlag("dcn_show_disconnected", showDisconnected);
    render();
  });
  const showDiscLabel = el("label", { for: "agents-show-disconnected" });
  const hiddenNote = el("span", { class: "muted" });
  const pager = createPager({ pageSize: SESSIONS_PER_PAGE, render: sessionRow, empty: () => t("agents.no_sessions") });
  const element = el("div", { class: "card fill-card" }, el("div", { class: "checks field" }, el("label", {}, showDisc, showDiscLabel), hiddenNote), pager.element);

  async function copyDisconnect(session) {
    try {
      const result = await api("GET", `/agents/sessions/${session.id}/disconnect-prompt`);
      await copyText(result.prompt);
    } catch (err) {
      toast(err.message, true);
    }
  }

  function isStale(session) {
    return session.status === "disconnected" && session.disconnected_at && Date.now() - new Date(session.disconnected_at).getTime() > STALE_DISCONNECTED_MS;
  }

  function sessionRow(session) {
    const project = session.project ? ` \u00B7 ${session.project}` : "";
    return el(
      "div",
      { class: "row session-row" },
      el(
        "div",
        { class: "row-main" },
        el("div", { class: "row-title" }, `${session.session_name}${project}`),
        el("div", { class: "muted" }, `${t(`agents.agent_${session.source}`)} \u00B7 ${t("agents.last_message")} ${formatTime(session.last_message_at)}`),
      ),
      el("span", { class: `chip ${session.status === "muted" ? "chip-warning" : ""}` }, t(`agents.status_${session.status}`)),
      el("button", { class: "btn btn-small", type: "button", onclick: () => shared.run(() => api("POST", `/agents/sessions/${session.id}/mute`, { muted: !session.muted }), session.muted ? "agents.unmuted" : "agents.muted") }, session.muted ? t("agents.unmute") : t("agents.mute")),
      el("button", { class: "btn btn-small btn-ghost", type: "button", title: t("agents.copy_disconnect"), "aria-label": t("agents.copy_disconnect"), onclick: () => copyDisconnect(session) }, icon("copy")),
      el("button", { class: "btn btn-ghost btn-small", type: "button", "aria-label": t("agents.remove"), title: t("agents.remove"), onclick: () => onRemove(session) }, icon("x")),
    );
  }

  function onRemove(session) {
    if (!window.confirm(t("agents.remove_confirm"))) {
      return;
    }
    shared.run(() => api("DELETE", `/agents/sessions/${session.id}`), "agents.removed");
  }

  function render() {
    showDiscLabel.textContent = t("agents.show_disconnected");
    if (!data) {
      return;
    }
    const visible = showDisconnected ? data.sessions : data.sessions.filter((s) => !isStale(s));
    const hidden = data.sessions.length - visible.length;
    hiddenNote.textContent = hidden > 0 ? `${t("agents.hidden_disconnected")} ${hidden}` : "";
    pager.set(visible);
  }

  return {
    element,
    update(newData) {
      data = newData;
      render();
    },
    refreshLanguage: render,
  };
}

/** Advanced: mute all, connect notices, disconnect prompt, token rotation, and the skill. */
function createAdvancedPanel(shared, wizard) {
  let data = null;

  const muteAll = toggleRow("agents-mute-all", () => shared.run(() => api("POST", "/agents/mute-all", { muted: muteAll.box.checked }), muteAll.box.checked ? "agents.muted_all_on" : "agents.muted_all_off"));
  const notify = toggleRow("agents-notify-connect", () => shared.run(() => api("POST", "/agents/notify-connect", { enabled: notify.box.checked }), notify.box.checked ? "agents.notify_connect_on" : "agents.notify_connect_off"));
  const generic = el("button", { class: "btn btn-small", type: "button", onclick: onGeneric });
  const genericTitle = el("div", { class: "row-title wrap" });
  const rotate = el("button", { class: "btn btn-small btn-danger", type: "button", onclick: onRotate });
  const rotateHint = el("span", { class: "muted" });
  const skillTitle = el("div", { class: "row-title" });
  const skillList = el("div", { class: "skill-list muted" });
  const skillInstall = el("button", { class: "btn btn-small", type: "button", onclick: () => changeSkill("install") });
  const skillRemove = el("button", { class: "btn btn-small btn-danger", type: "button", onclick: onRemoveSkill });

  const element = el(
    "div",
    { class: "card adv-card" },
    muteAll.row,
    notify.row,
    el("div", { class: "row" }, el("span", { class: "row-main" }, genericTitle), generic),
    el("div", { class: "row" }, el("span", { class: "row-main row-title wrap rotate-title" }, rotateHint), rotate),
    el("div", { class: "skill-block" }, skillTitle, skillList, el("div", { class: "form-actions" }, skillInstall, skillRemove)),
  );

  async function onGeneric() {
    try {
      await copyText(await wizard.genericDisconnect());
    } catch (err) {
      toast(err.message, true);
    }
  }

  function onRotate() {
    if (!window.confirm(t("agents.rotate_confirm"))) {
      return;
    }
    shared.run(() => api("POST", "/agents/rotate-token"), "agents.rotated");
  }

  function changeSkill(action) {
    shared.run(() => api("POST", "/agents/skill", { action }), action === "install" ? "agents.skill_installed" : "agents.skill_removed");
  }

  function onRemoveSkill() {
    if (window.confirm(t("agents.skill_remove_confirm"))) {
      changeSkill("remove");
    }
  }

  function skillLine(target) {
    let state = t("agents.skill_missing");
    if (!target.available) {
      state = t("agents.skill_unavailable");
    } else if (target.installed) {
      state = target.up_to_date ? t("agents.skill_current") : t("agents.skill_outdated");
    }
    const folder = el("span", { class: "number skill-path" }, target.path);
    return el("div", { class: "skill-line" }, folder, el("span", { class: "chip" }, state));
  }

  function render() {
    muteAll.title.textContent = t("agents.mute_all");
    muteAll.hint.textContent = t("agents.mute_all_hint");
    notify.title.textContent = t("agents.notify_connect");
    notify.hint.textContent = t("agents.notify_connect_hint");
    generic.textContent = t("agents.copy");
    genericTitle.textContent = t("agents.copy_generic_disconnect");
    rotateHint.textContent = t("agents.rotate_title");
    rotate.textContent = t("agents.rotate");
    skillTitle.textContent = t("agents.skill_title");
    skillInstall.textContent = t("agents.skill_install");
    skillRemove.textContent = t("agents.skill_remove");
    if (data) {
      muteAll.box.checked = data.mute_all;
      notify.box.checked = data.notify_connect;
      skillList.replaceChildren(...data.skill.map(skillLine));
    }
  }

  return {
    element,
    update(newData) {
      data = newData;
      render();
    },
    refreshLanguage: render,
  };
}

export function createAgentsView(root) {
  let data = null;

  const shared = {
    async run(action, successKey) {
      try {
        await action();
        if (successKey) {
          toast(t(successKey));
        }
        await load();
      } catch (err) {
        toast(err.message, true, 5000);
      }
    },
  };

  const wizardRoot = el("div", { class: "subpanel" });
  const wizard = createWizard(wizardRoot);
  const connected = createConnectedPanel(shared);
  const advanced = createAdvancedPanel(shared, wizard);
  const connectedPanel = el("div", { class: "subpanel" }, connected.element);
  const advancedPanel = el("div", { class: "subpanel" }, advanced.element);

  const tabs = createSubtabs(root, [
    { key: "new", label: () => t("agents.sub_new"), panel: wizardRoot, show: () => wizard.show() },
    { key: "connected", label: () => t("agents.sub_connected"), panel: connectedPanel, show: load },
    { key: "advanced", label: () => t("agents.sub_advanced"), panel: advancedPanel, show: load },
  ]);

  async function load() {
    try {
      data = await api("GET", "/agents");
      connected.update(data);
      advanced.update(data);
    } catch (err) {
      toast(err.message, true);
    }
  }

  return {
    show() {
      tabs.select(tabs.current);
    },
    select: (key) => tabs.select(key),
    // The poll refreshes the live lists only; the wizard keeps what the user is choosing.
    refresh() {
      if (tabs.current !== "new") {
        return load();
      }
      return undefined;
    },
    refreshLanguage() {
      tabs.refreshLabels();
      wizard.refreshLanguage();
      connected.refreshLanguage();
      advanced.refreshLanguage();
    },
  };
}
