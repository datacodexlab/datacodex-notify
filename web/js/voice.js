import { api, apiBlob } from "./api.js";
import { el, icon, isDirty, toast, trackEdits } from "./dom.js";
import { language, t } from "./i18n.js";
import { createModelsSection } from "./voice-models.js";
import { createGuard, createPager, createSubtabs } from "./widgets.js";

const LEVELS = ["info", "warning", "critical"];
const MODES = ["text", "voice", "both"];
const VOICES = ["Kore", "Charon"];
const SPEEDS = ["normal", "fast", "very_fast"];
const LONG_TOAST_MS = 6000;
const KEYS_PER_PAGE = 3;

export function createVoiceView(root) {
  let overview = null;

  // ---- Keys
  const statusLines = el("div", { class: "muted" });
  const keyPager = createPager({ pageSize: KEYS_PER_PAGE, render: keyRow, empty: () => t("voice.no_keys") });
  const keyLabel = el("input", { type: "text", id: "key-label", autocomplete: "off" });
  const keyValue = el("input", { type: "password", id: "key-value", autocomplete: "off", dir: "ltr" });
  const keyLabelText = el("label", { for: "key-label" });
  const keyValueText = el("label", { for: "key-value" });
  const addKeyButton = el("button", { class: "btn btn-primary btn-small", type: "button", onclick: onAddKey });
  const keysPanel = el(
    "div",
    { class: "subpanel" },
    el("div", { class: "card fill-card" }, statusLines, keyPager.element),
    el(
      "div",
      { class: "card" },
      el("div", { class: "pair-fields" }, el("div", { class: "field" }, keyLabelText, keyLabel), el("div", { class: "field" }, keyValueText, keyValue)),
      el("div", { class: "form-actions" }, addKeyButton),
    ),
  );

  // ---- Voice & speed
  const voiceSelect = el("select", { id: "voice-select", onchange: () => run(() => api("PUT", "/voice", { voice: voiceSelect.value }), "voice.saved") });
  const voiceLabel = el("label", { for: "voice-select" });
  const speedSelect = el("select", { id: "speed-select", onchange: () => run(() => api("PUT", "/voice", { speed: speedSelect.value }), "voice.saved") });
  const speedLabel = el("label", { for: "speed-select" });
  const introBox = el("input", { type: "checkbox", id: "spoken-intro" });
  introBox.addEventListener("change", () => run(() => api("PUT", "/voice", { spoken_intro: introBox.checked }), "voice.saved"));
  const introLabel = el("label", { for: "spoken-intro" });
  const playButton = el("button", { class: "btn btn-small", type: "button", onclick: onPlaySample });
  const sendButton = el("button", { class: "btn btn-small", type: "button", onclick: onSendTest });
  const testHint = el("p", { class: "muted" });
  const player = el("audio", { hidden: true });

  const modesTitle = el("h2", {});
  const modeRows = LEVELS.map((level) => {
    const select = el("select", { id: `mode-${level}` });
    const label = el("label", { for: `mode-${level}`, "data-level": level });
    return { level, select, label, field: el("div", { class: "field" }, label, select) };
  });
  const saveModes = el("button", { class: "btn btn-primary btn-small", type: "button", onclick: onSaveModes });
  const modeSelects = modeRows.map((r) => r.select);
  trackEdits(...modeSelects);
  const modesGuard = createGuard(modeSelects);

  const voicePanel = el(
    "div",
    { class: "subpanel" },
    el(
      "div",
      { class: "card" },
      el("div", { class: "pair-fields" }, el("div", { class: "field" }, voiceLabel, voiceSelect), el("div", { class: "field" }, speedLabel, speedSelect)),
      el("div", { class: "checks field" }, el("label", {}, introBox, introLabel)),
      el("div", { class: "form-actions" }, playButton, sendButton),
      testHint,
      player,
    ),
    el("div", { class: "card" }, modesTitle, el("div", { class: "triple-fields" }, ...modeRows.map((r) => r.field)), el("div", { class: "form-actions" }, saveModes, modesGuard.marker)),
  );

  // ---- Models
  const models = createModelsSection(load);
  const modelsPanel = el("div", { class: "subpanel" }, models.element);

  const tabs = createSubtabs(root, [
    { key: "keys", label: () => t("voice.sub_keys"), panel: keysPanel, show: load },
    { key: "voice", label: () => t("voice.sub_voice"), panel: voicePanel, show: load },
    { key: "models", label: () => t("voice.sub_models"), panel: modelsPanel, show: load },
  ]);

  async function load() {
    try {
      overview = await api("GET", "/voice");
      render();
    } catch (err) {
      toast(err.message, true);
    }
  }

  async function run(action, successKey) {
    try {
      await action();
      if (successKey) {
        toast(t(successKey));
      }
      await load();
    } catch (err) {
      toast(err.message, true, LONG_TOAST_MS);
    }
  }

  function onAddKey() {
    const key = keyValue.value.trim();
    if (!key) {
      toast(t("voice.key_required"), true);
      return;
    }
    run(async () => {
      await api("POST", "/voice/keys", { label: keyLabel.value.trim(), key });
      keyValue.value = "";
      keyLabel.value = "";
    }, "voice.key_added");
  }

  async function onTestKey(info) {
    try {
      const result = await api("POST", `/voice/keys/${info.id}/test`);
      if (!result.valid) {
        toast(result.message || t("voice.key_invalid"), true, LONG_TOAST_MS);
      } else if (result.rate_limited) {
        toast(`${t("voice.key_valid_limited")} ${result.message}`, false, LONG_TOAST_MS);
      } else {
        toast(t("voice.key_valid"));
      }
    } catch (err) {
      toast(err.message, true, LONG_TOAST_MS);
    }
  }

  function onDeleteKey(info) {
    if (!window.confirm(t("voice.key_delete_confirm", { label: info.label }))) {
      return;
    }
    run(() => api("DELETE", `/voice/keys/${info.id}`), "voice.key_deleted");
  }

  async function onPlaySample() {
    playButton.disabled = true;
    try {
      const blob = await apiBlob("POST", "/voice/sample");
      if (player.src) {
        URL.revokeObjectURL(player.src);
      }
      player.src = URL.createObjectURL(blob);
      await player.play();
    } catch (err) {
      toast(err.message, true, LONG_TOAST_MS);
    } finally {
      playButton.disabled = false;
      await load();
    }
  }

  async function onSendTest() {
    try {
      await api("POST", "/voice/test");
      toast(t("voice.test_queued"));
    } catch (err) {
      toast(err.message, true, LONG_TOAST_MS);
    }
  }

  function onSaveModes() {
    const modes = {};
    for (const row of modeRows) {
      modes[row.level] = row.select.value;
    }
    modesGuard.clean();
    run(() => api("PUT", "/voice", { modes }), "voice.saved");
  }

  function keyRow(info) {
    const added = new Date(info.added_at).toLocaleDateString(language() === "ar" ? "ar-EG" : "en-GB");
    return el(
      "div",
      { class: "row" },
      el(
        "div",
        { class: "row-main" },
        el("div", { class: "row-title" }, info.label),
        el("div", { class: "muted number" }, `${info.masked} \u00B7 ${t("voice.added")} ${added}`),
      ),
      el("button", { class: "btn btn-small", type: "button", onclick: () => onTestKey(info) }, icon("check"), t("voice.key_test")),
      el("button", { class: "btn btn-ghost btn-small", type: "button", "aria-label": t("actions.delete"), onclick: () => onDeleteKey(info) }, icon("trash")),
    );
  }

  function renderStatic() {
    keyLabelText.textContent = t("voice.key_label");
    keyValueText.textContent = t("voice.key_value");
    addKeyButton.textContent = t("voice.key_add");
    voiceLabel.textContent = t("voice.voice_label");
    speedLabel.textContent = t("voice.speed_label");
    introLabel.textContent = t("voice.intro_label");
    playButton.textContent = t("voice.play_sample");
    sendButton.textContent = t("voice.send_test");
    modesTitle.textContent = t("voice.modes_title");
    saveModes.textContent = t("actions.save");
    for (const row of modeRows) {
      row.label.textContent = t(`levels.${row.level}`);
    }
    modesGuard.update();
    tabs.refreshLabels();
  }

  function fillSelect(select, values, labelOf, current) {
    const wanted = isDirty(select) ? select.value : current;
    select.replaceChildren(...values.map((v) => el("option", { value: v }, labelOf(v))));
    select.value = wanted;
  }

  function formatTime(iso) {
    return new Date(iso).toLocaleTimeString(language() === "ar" ? "ar-EG" : "en-GB", { hour: "2-digit", minute: "2-digit" });
  }

  function render() {
    renderStatic();
    if (!overview) {
      return;
    }
    const { status, keys } = overview;

    const lines = [t("voice.key_count", { count: String(status.key_count) })];
    if (status.next_available_at) {
      lines.push(t("voice.cooling_down", { time: formatTime(status.next_available_at) }));
    }
    statusLines.textContent = lines.join(" \u00B7 ");
    keyPager.set(keys);

    fillSelect(voiceSelect, VOICES, (v) => v, status.voice);
    fillSelect(speedSelect, SPEEDS, (v) => t(`voice.speed_${v}`), status.speed || "normal");
    for (const row of modeRows) {
      fillSelect(row.select, MODES, (m) => t(`voice.mode_${m}`), status.modes[row.level]);
    }
    introBox.checked = overview.spoken_intro !== false;

    playButton.disabled = status.key_count === 0;
    sendButton.disabled = !status.available;
    if (status.key_count === 0) {
      testHint.textContent = t("voice.hint_no_key");
    } else if (!status.ffmpeg_found) {
      testHint.textContent = t("voice.hint_no_ffmpeg");
    } else {
      testHint.textContent = "";
    }
    models.update(status);
  }

  return {
    show() {
      tabs.select(tabs.current);
    },
    refreshLanguage() {
      if (overview) {
        render();
      } else {
        renderStatic();
      }
    },
  };
}
