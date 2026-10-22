import { api } from "./api.js";
import { el, isDirty, markClean, toast, trackEdits } from "./dom.js";
import { t } from "./i18n.js";

const NONE = "none";

/**
 * The model chooser: shows the effective pair, lists the account's TTS models on
 * demand, saves a choice after Google validates it, and resets to the defaults.
 */
export function createModelsSection(onChanged) {
  let status = null;
  let models = [];

  const title = el("h2", {});
  const current = el("p", { class: "muted" });
  const unavailable = el("p", { class: "status-fail muted" });
  const listButton = el("button", { class: "btn", type: "button", onclick: onList });
  const primary = el("select", { id: "model-primary" });
  const fallback = el("select", { id: "model-fallback" });
  const primaryLabel = el("label", { for: "model-primary" });
  const fallbackLabel = el("label", { for: "model-fallback" });
  const save = el("button", { class: "btn btn-primary", type: "button", onclick: onSave });
  const reset = el("button", { class: "btn", type: "button", onclick: onReset });

  trackEdits(primary, fallback);

  const picker = el(
    "div",
    { hidden: true },
    el("div", { class: "field" }, primaryLabel, primary),
    el("div", { class: "field" }, fallbackLabel, fallback),
    el("div", { class: "form-actions" }, save),
  );
  const root = el("div", { class: "card" }, title, current, unavailable, picker, el("div", { class: "form-actions" }, listButton, reset));

  function label(model) {
    return model.display_name && model.display_name !== model.id ? `${model.display_name} (${model.id})` : model.id;
  }

  function fillSelects() {
    const ids = new Set(models.map((m) => m.id));
    const extra = [status.models.primary, status.models.fallback].filter((id) => id && !ids.has(id));
    const options = [...models, ...extra.map((id) => ({ id, display_name: id }))];

    const wantedPrimary = isDirty(primary) ? primary.value : status.models.primary;
    const wantedFallback = isDirty(fallback) ? fallback.value : status.models.fallback || NONE;
    primary.replaceChildren(...options.map((m) => el("option", { value: m.id }, label(m))));
    fallback.replaceChildren(
      el("option", { value: NONE }, t("voice.model_none")),
      ...options.map((m) => el("option", { value: m.id }, label(m))),
    );
    primary.value = wantedPrimary;
    fallback.value = wantedFallback;
  }

  async function onList() {
    listButton.disabled = true;
    try {
      const data = await api("GET", "/voice/models");
      models = data.models;
      picker.hidden = false;
      fillSelects();
    } catch (err) {
      toast(err.message, true, 6000);
    } finally {
      listButton.disabled = false;
    }
  }

  async function onSave() {
    if (primary.value === fallback.value) {
      toast(t("voice.model_same"), true);
      return;
    }
    save.disabled = true;
    try {
      await api("PUT", "/voice/models", { primary: primary.value, fallback: fallback.value });
      toast(t("voice.model_saved"));
      markClean(primary, fallback);
      picker.hidden = true;
      await onChanged();
    } catch (err) {
      toast(err.message, true, 6000);
    } finally {
      save.disabled = false;
    }
  }

  async function onReset() {
    try {
      await api("DELETE", "/voice/models");
      toast(t("voice.model_reset_done"));
      markClean(primary, fallback);
      picker.hidden = true;
      await onChanged();
    } catch (err) {
      toast(err.message, true);
    }
  }

  function renderStatic() {
    title.textContent = t("voice.models_title");
    listButton.textContent = t("voice.show_models");
    primaryLabel.textContent = t("voice.model_primary");
    fallbackLabel.textContent = t("voice.model_fallback");
    save.textContent = t("actions.save");
    reset.textContent = t("voice.model_reset");
  }

  return {
    element: root,
    update(newStatus) {
      status = newStatus;
      renderStatic();
      const pair = status.models;
      current.textContent = t("voice.model_current", {
        primary: pair.primary,
        fallback: pair.fallback || t("voice.model_none"),
      });
      const missing = Object.keys(status.unavailable_models || {});
      unavailable.hidden = missing.length === 0;
      unavailable.textContent = t("voice.model_unavailable", { models: missing.join(", ") });
      reset.disabled = !pair.custom;
      if (!picker.hidden) {
        fillSelects();
      }
    },
  };
}
