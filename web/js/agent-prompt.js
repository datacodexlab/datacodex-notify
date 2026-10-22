import { api } from "./api.js";
import { el, toast } from "./dom.js";
import { t } from "./i18n.js";

const AGENTS = ["claude-code", "codex", "gemini", "other"];
const SCOPES = ["global", "project"];
const TEMPLATES = ["calm", "normal", "detailed"];
const LANGUAGES = ["egyptian", "arabic", "english"];
const ACTIVATIONS = ["on_request", "suggest_long", "always"];
const LEVELS = ["info", "warning", "critical"];
const MODES = ["text", "voice", "both"];
const PREVIEW_DELAY_MS = 350;
const STEP_COUNT = 3;

export async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    toast(t("agents.copied"));
  } catch {
    toast(t("agents.copy_failed"), true);
  }
}

function fillSelect(node, values, labelOf, current) {
  node.replaceChildren(...values.map((v) => el("option", { value: v }, labelOf(v))));
  node.value = current;
}

function lines(text) {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "");
}

/**
 * The "New connection" wizard: (1) agent and scope, (2) how much to report,
 * (3) copy the prompt. Every control stays in the page, so the prompt always
 * reflects all choices, and the last choices are remembered by the server.
 */
export function createWizard(root) {
  let options = null;
  let templates = {};
  let modeDefaults = {};
  let timer = null;
  let step = 1;
  let generic = "";

  const stepLabel = el("div", { class: "wizard-step" });
  const dots = el("div", { class: "wizard-dots" }, ...[1, 2, 3].map((n) => el("span", { class: "wizard-dot", "data-n": String(n) })));

  // step 1
  const agent = el("select", { id: "ap-agent" });
  const otherFile = el("input", { type: "text", id: "ap-other", autocomplete: "off", dir: "ltr", placeholder: "RULES.md" });
  const scope = el("select", { id: "ap-scope" });
  const labels = {};
  function field(key, input) {
    labels[key] = el("label", { for: input.id });
    return el("div", { class: "field" }, labels[key], input);
  }
  const step1 = el("div", { class: "wizard-body" }, field("agent", agent), field("other", otherFile), field("scope", scope));

  // step 2
  const template = el("input", { type: "hidden", id: "ap-template" });
  const cards = TEMPLATES.map((key) => {
    const button = el("button", { class: "tpl-card", type: "button", "data-template": key, onclick: () => chooseTemplate(key) });
    return button;
  });
  const eventsBox = el("div", { class: "events-table" });
  const customize = el("details", { class: "expander" }, el("summary", {}), el("div", { class: "expander-body events-body" }, eventsBox));
  const step2 = el("div", { class: "wizard-body" }, el("div", { class: "tpl-row" }, ...cards), customize);

  // step 3
  const copy = el("button", { class: "btn btn-primary btn-big", type: "button", onclick: () => onCopy() });
  const language = el("select", { id: "ap-language" });
  const activation = el("select", { id: "ap-activation" });
  const showPrompt = el("button", { class: "btn btn-small btn-ghost", type: "button", onclick: () => togglePreview() });
  const preview = el("textarea", { id: "ap-preview", rows: "4", readonly: true, dir: "ltr", hidden: true });
  const problem = el("p", { class: "status-fail muted", hidden: true });
  const heartbeat = el("input", { type: "checkbox", id: "ap-heartbeat" });
  const heartbeatLabel = el("label", { for: "ap-heartbeat" });
  const heartbeatEvery = el("input", { type: "number", id: "ap-every", min: "30", step: "30", dir: "ltr" });
  const activate = el("textarea", { id: "ap-activate", rows: "2", dir: "auto" });
  const deactivate = el("textarea", { id: "ap-deactivate", rows: "2", dir: "auto" });
  const extra = el("textarea", { id: "ap-extra", rows: "2", dir: "auto" });
  const more = el(
    "details",
    { class: "expander" },
    el("summary", {}),
    el(
      "div",
      { class: "expander-body more-body" },
      el("div", { class: "checks field" }, el("label", {}, heartbeat, heartbeatLabel)),
      field("every", heartbeatEvery),
      field("activate", activate),
      field("deactivate", deactivate),
      field("extra", extra),
    ),
  );
  const step3 = el(
    "div",
    { class: "wizard-body" },
    copy,
    el("div", { class: "stack-fields" }, field("language", language), field("activation", activation)),
    showPrompt,
    preview,
    problem,
    more,
  );

  // navigation
  const back = el("button", { class: "btn", type: "button", onclick: () => go(-1) });
  const next = el("button", { class: "btn btn-primary", type: "button", onclick: () => go(1) });
  const nav = el("div", { class: "form-actions wizard-nav" }, back, next);

  root.append(el("div", { class: "card wizard-card" }, el("div", { class: "wizard-head" }, stepLabel, dots), step1, step2, step3, nav));

  for (const node of [agent, otherFile, scope, language, activation, heartbeat, heartbeatEvery, activate, deactivate, extra]) {
    node.addEventListener("input", onChange);
  }

  function chooseTemplate(key) {
    template.value = key;
    renderCards();
    renderEvents();
    onChange();
  }

  function renderCards() {
    for (const card of cards) {
      const key = card.dataset.template;
      card.classList.toggle("selected", template.value === key);
      card.setAttribute("aria-pressed", String(template.value === key));
      card.replaceChildren(el("strong", {}, t(`agents.card_${key}`)), el("span", { class: "muted" }, t(`agents.card_${key}_desc`)));
    }
  }

  function read() {
    const levels = {};
    const modes = {};
    for (const row of eventsBox.querySelectorAll("select[data-event]")) {
      if (row.value !== row.dataset.defaultLevel) {
        levels[row.dataset.event] = row.value;
      }
    }
    for (const row of eventsBox.querySelectorAll("select[data-mode-event]")) {
      if (row.value !== defaultModeFor(row.dataset.modeEvent)) {
        modes[row.dataset.modeEvent] = row.value;
      }
    }
    return {
      agent: agent.value,
      other_file: otherFile.value.trim(),
      scope: scope.value,
      template: template.value,
      levels,
      modes,
      heartbeat: heartbeat.checked,
      heartbeat_every_s: Number(heartbeatEvery.value) || 0,
      language: language.value,
      activation: activation.value,
      activate_phrases: lines(activate.value),
      deactivate_phrases: lines(deactivate.value),
      extra: extra.value,
    };
  }

  function onChange() {
    otherFile.closest(".field").hidden = agent.value !== "other";
    heartbeatEvery.closest(".field").hidden = !heartbeat.checked;
    clearTimeout(timer);
    timer = setTimeout(refreshPreview, PREVIEW_DELAY_MS);
  }

  async function refreshPreview() {
    const current = read();
    try {
      const result = await api("POST", "/agents/prompt", current);
      preview.value = result.connect;
      generic = result.disconnect_generic;
      problem.hidden = true;
      copy.disabled = false;
      await api("PUT", "/agents/prompt-options", current);
    } catch (err) {
      problem.textContent = err.message;
      problem.hidden = false;
      copy.disabled = true;
    }
  }

  async function onCopy() {
    clearTimeout(timer);
    await refreshPreview();
    if (!copy.disabled) {
      await copyText(preview.value);
    }
  }

  function togglePreview() {
    preview.hidden = !preview.hidden;
    showPrompt.textContent = t(preview.hidden ? "agents.show_prompt" : "agents.hide_prompt");
  }

  function defaultModeFor(event) {
    const levelSelect = eventsBox.querySelector(`select[data-event="${event}"]`);
    const defaults = modeDefaults[template.value] || {};
    return (levelSelect && defaults[levelSelect.value]) || "text";
  }

  function renderEvents() {
    const events = templates[template.value] || [];
    const savedLevels = (options && options.levels) || {};
    const savedModes = (options && options.modes) || {};
    eventsBox.replaceChildren(
      ...events.map((info) => {
        const levelSelect = el("select", { "data-event": info.event, "data-default-level": info.default_level, "aria-label": t(`agents.event_${info.event}`) });
        fillSelect(levelSelect, LEVELS, (l) => t(`levels.${l}`), savedLevels[info.event] || info.default_level);
        const modeSelect = el("select", { "data-mode-event": info.event, "aria-label": `${t("agents.mode")}: ${t(`agents.event_${info.event}`)}` });
        const defaults = modeDefaults[template.value] || {};
        fillSelect(modeSelect, MODES, (m) => t(`voice.mode_${m}`), savedModes[info.event] || defaults[levelSelect.value] || "text");
        let previousDefault = defaults[levelSelect.value];
        levelSelect.addEventListener("input", () => {
          const nextDefault = (modeDefaults[template.value] || {})[levelSelect.value];
          if (modeSelect.value === previousDefault) {
            modeSelect.value = nextDefault;
          }
          previousDefault = nextDefault;
          onChange();
        });
        modeSelect.addEventListener("input", onChange);
        return el("div", { class: "event-row" }, el("span", { class: "row-main" }, t(`agents.event_${info.event}`)), levelSelect, modeSelect);
      }),
    );
  }

  function go(delta) {
    step = Math.min(Math.max(step + delta, 1), STEP_COUNT);
    renderStep();
  }

  function renderStep() {
    step1.hidden = step !== 1;
    step2.hidden = step !== 2;
    step3.hidden = step !== 3;
    stepLabel.textContent = `${t("agents.step", { n: String(step) })} \u00B7 ${t(`agents.step_${step}_title`)}`;
    for (const dot of dots.children) {
      dot.classList.toggle("active", Number(dot.dataset.n) <= step);
    }
    back.hidden = step === 1;
    next.hidden = step === STEP_COUNT;
  }

  function renderStatic() {
    for (const [key, text] of Object.entries({
      agent: "agents.agent",
      other: "agents.other_file",
      scope: "agents.scope",
      every: "agents.heartbeat_every",
      language: "agents.language",
      activation: "agents.activation",
      activate: "agents.activate_phrases",
      deactivate: "agents.deactivate_phrases",
      extra: "agents.extra",
    })) {
      labels[key].textContent = t(text);
    }
    heartbeatLabel.textContent = t("agents.heartbeat");
    copy.textContent = t("agents.copy_prompt");
    showPrompt.textContent = t(preview.hidden ? "agents.show_prompt" : "agents.hide_prompt");
    customize.querySelector("summary").textContent = t("agents.customize");
    more.querySelector("summary").textContent = t("agents.more_options");
    back.textContent = t("common.back");
    next.textContent = t("common.next");

    const current = options || {};
    fillSelect(agent, AGENTS, (v) => t(`agents.agent_${v}`), current.agent || "claude-code");
    fillSelect(scope, SCOPES, (v) => t(`agents.scope_${v}`), current.scope || "global");
    fillSelect(language, LANGUAGES, (v) => t(`agents.language_${v}`), current.language || "egyptian");
    fillSelect(activation, ACTIVATIONS, (v) => t(`agents.activation_${v}`), current.activation || "on_request");
    renderCards();
    renderStep();
  }

  function fill() {
    template.value = options.template || "normal";
    renderStatic();
    otherFile.value = options.other_file || "";
    heartbeat.checked = options.heartbeat;
    heartbeatEvery.value = options.heartbeat_every_s || 600;
    activate.value = (options.activate_phrases || []).join("\n");
    deactivate.value = (options.deactivate_phrases || []).join("\n");
    extra.value = options.extra || "";
    renderEvents();
    onChange();
  }

  return {
    async show() {
      try {
        const data = await api("GET", "/agents/prompt-options");
        options = data.options;
        templates = data.templates;
        modeDefaults = data.mode_defaults || {};
        fill();
      } catch (err) {
        toast(err.message, true);
      }
    },
    /** The generic "remove it from any agent" prompt, built from the saved choices. */
    async genericDisconnect() {
      const data = await api("GET", "/agents/prompt-options");
      const result = await api("POST", "/agents/prompt", data.options);
      generic = result.disconnect_generic;
      return generic;
    },
    refreshLanguage() {
      if (!options) {
        return;
      }
      options = read();
      renderStatic();
      renderEvents();
    },
  };
}
