import { api } from "./api.js";
import { el, setIfClean, toast, trackEdits } from "./dom.js";
import { language, t } from "./i18n.js";
import { createRecipientsPanel } from "./recipients.js";
import { createGuard, createSubtabs } from "./widgets.js";

/**
 * Settings: recipients, then general settings (interface language, start with
 * Windows, ffmpeg path, about) and an Advanced section with the dangerous action.
 */
export function createSettingsView(root, { onLanguage, version }) {
  const recipients = createRecipientsPanel();

  // ---- General
  const langLabel = el("label", { for: "ui-language" });
  const langSelect = el("select", { id: "ui-language", onchange: () => onLanguage(langSelect.value) });
  const autoBox = el("input", { type: "checkbox", id: "autostart-toggle" });
  autoBox.addEventListener("change", onAutostart);
  const autoLabel = el("label", { for: "autostart-toggle" });
  const autoRow = el("div", { class: "checks field" }, el("label", {}, autoBox, autoLabel));
  const ffmpegInput = el("input", { type: "text", id: "ffmpeg-path", autocomplete: "off", dir: "ltr" });
  const ffmpegLabel = el("label", { for: "ffmpeg-path" });
  const saveFFmpeg = el("button", { class: "btn btn-small btn-primary", type: "button", onclick: onSaveFFmpeg });
  trackEdits(ffmpegInput);
  const ffmpegGuard = createGuard([ffmpegInput]);
  const about = el("p", { class: "muted" });
  const unlink = el("button", { class: "btn btn-small btn-danger", type: "button", onclick: onUnlink });
  const unlinkHint = el("p", { class: "muted" });
  const advanced = el("details", { class: "expander" }, el("summary", {}), el("div", { class: "expander-body" }, unlinkHint, unlink));

  const generalPanel = el(
    "div",
    { class: "subpanel" },
    el(
      "div",
      { class: "card" },
      el("div", { class: "field" }, langLabel, langSelect),
      autoRow,
      el("div", { class: "field" }, ffmpegLabel, el("div", { class: "inline-save" }, ffmpegInput, saveFFmpeg), ffmpegGuard.marker),
      about,
      advanced,
    ),
  );

  const tabs = createSubtabs(root, [
    { key: "recipients", label: () => t("settings.sub_recipients"), panel: recipients.element, show: () => recipients.show() },
    { key: "general", label: () => t("settings.sub_general"), panel: generalPanel, show: loadGeneral },
  ]);

  let voice = null;
  let autostart = null;

  async function loadGeneral() {
    try {
      const [rules, overview] = await Promise.all([api("GET", "/rules"), api("GET", "/voice")]);
      autostart = rules.autostart;
      voice = overview.status;
      renderGeneral();
    } catch (err) {
      toast(err.message, true);
    }
  }

  async function onAutostart() {
    const wanted = autoBox.checked;
    try {
      await api("PUT", "/autostart", { enabled: wanted });
      toast(t(wanted ? "rules.autostart_on" : "rules.autostart_off"));
    } catch (err) {
      toast(err.message, true);
    }
    await loadGeneral();
  }

  async function onSaveFFmpeg() {
    ffmpegGuard.clean();
    try {
      await api("PUT", "/voice", { ffmpeg_path: ffmpegInput.value.trim() });
      toast(t("voice.saved"));
    } catch (err) {
      toast(err.message, true);
    }
    await loadGeneral();
  }

  async function onUnlink() {
    if (!window.confirm(t("connection.unlink_confirm"))) {
      return;
    }
    try {
      await api("POST", "/unlink");
      toast(t("connection.unlinked"));
    } catch (err) {
      toast(err.message, true);
    }
  }

  function renderStatic() {
    langLabel.textContent = t("settings.ui_language");
    langSelect.replaceChildren(el("option", { value: "ar" }, t("language.switch_ar")), el("option", { value: "en" }, t("language.switch_en")));
    langSelect.value = language();
    autoLabel.textContent = t("rules.autostart_label");
    ffmpegLabel.textContent = t("voice.ffmpeg_label");
    saveFFmpeg.textContent = t("actions.save");
    about.textContent = `${t("settings.about")} \u00B7 ${t("footer.version")} ${version()}`;
    advanced.querySelector("summary").textContent = t("settings.advanced");
    unlinkHint.textContent = t("settings.unlink_hint");
    unlink.textContent = t("connection.unlink");
    ffmpegGuard.update();
    tabs.refreshLabels();
  }

  function renderGeneral() {
    renderStatic();
    if (autostart) {
      autoRow.hidden = !autostart.available;
      autoBox.checked = autostart.enabled;
    }
    if (voice) {
      setIfClean(ffmpegInput, voice.ffmpeg_configured);
      ffmpegInput.placeholder = voice.ffmpeg_path || "ffmpeg.exe";
    }
  }

  return {
    show() {
      tabs.select(tabs.current);
    },
    select: (key) => tabs.select(key),
    refreshLanguage() {
      recipients.refreshLanguage();
      renderGeneral();
    },
  };
}
