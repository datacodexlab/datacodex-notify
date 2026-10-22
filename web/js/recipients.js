import { api } from "./api.js";
import { el, icon, markClean, toast, trackEdits } from "./dom.js";
import { t } from "./i18n.js";
import { createGuard, createPager } from "./widgets.js";

const E164 = /^\+[1-9]\d{7,14}$/;
const LEVELS = [
  { key: "info", bit: 1 },
  { key: "warning", bit: 2 },
  { key: "critical", bit: 4 },
];
const PER_PAGE = 2;

export function maskNumber(phone) {
  if (phone.length <= 6) {
    return phone;
  }
  return `${phone.slice(0, 4)}${String.fromCharCode(0x2022).repeat(phone.length - 6)}${phone.slice(-2)}`;
}

/** Recipients: a paginated list and an add/edit form with an unsaved-change marker. */
export function createRecipientsPanel() {
  let editingId = null;
  let recipients = [];

  const name = el("input", { type: "text", id: "rcp-name", autocomplete: "off" });
  const phone = el("input", { type: "tel", id: "rcp-phone", autocomplete: "off", dir: "ltr", placeholder: "+201000000001" });
  const checks = LEVELS.map((level) => ({
    ...level,
    box: el("input", { type: "checkbox", checked: true }),
  }));
  const save = el("button", { class: "btn btn-primary btn-small", type: "button", onclick: onSave });
  const cancel = el("button", { class: "btn btn-small", type: "button", onclick: resetForm, hidden: true });
  const formTitle = el("h2", {});
  const labels = {
    name: el("label", { for: "rcp-name" }),
    phone: el("label", { for: "rcp-phone" }),
  };
  const fields = [name, phone, ...checks.map((c) => c.box)];
  trackEdits(...fields);
  const guard = createGuard(fields);

  const form = el(
    "div",
    { class: "card" },
    formTitle,
    el("div", { class: "pair-fields" }, el("div", { class: "field" }, labels.name, name), el("div", { class: "field" }, labels.phone, phone)),
    el("div", { class: "checks field" }, ...checks.map((c) => el("label", {}, c.box, el("span", { "data-level": c.key })))),
    el("div", { class: "form-actions" }, save, cancel, guard.marker),
  );
  const pager = createPager({ pageSize: PER_PAGE, render: row, empty: () => t("recipients.empty") });
  const list = el("div", { class: "card fill-card" }, pager.element);
  const element = el("div", { class: "subpanel" }, list, form);

  function resetForm() {
    editingId = null;
    name.value = "";
    phone.value = "";
    checks.forEach((c) => {
      c.box.checked = true;
    });
    cancel.hidden = true;
    guard.clean();
    renderStatic();
  }

  function levelMask() {
    return checks.reduce((mask, c) => (c.box.checked ? mask | c.bit : mask), 0);
  }

  async function onSave() {
    const number = phone.value.trim();
    if (!E164.test(number)) {
      toast(t("recipients.invalid_phone"), true);
      return;
    }
    const levels = levelMask();
    if (levels === 0) {
      toast(t("recipients.no_levels"), true);
      return;
    }
    const body = { name: name.value.trim() || number, phone_e164: number, levels };
    try {
      if (editingId) {
        const current = recipients.find((r) => r.id === editingId);
        await api("PUT", `/recipients/${editingId}`, { ...body, enabled: current ? current.enabled : true });
      } else {
        await api("POST", "/recipients", body);
      }
      toast(t("recipients.saved"));
      resetForm();
      await load();
    } catch (err) {
      toast(err.message, true);
    }
  }

  async function toggle(recipient, enabled) {
    try {
      await api("PUT", `/recipients/${recipient.id}`, {
        name: recipient.name,
        phone_e164: recipient.phone_e164,
        levels: recipient.levels,
        enabled,
      });
      await load();
    } catch (err) {
      toast(err.message, true);
    }
  }

  async function remove(recipient) {
    if (!window.confirm(t("recipients.delete_confirm", { name: recipient.name }))) {
      return;
    }
    try {
      await api("DELETE", `/recipients/${recipient.id}`);
      await load();
    } catch (err) {
      toast(err.message, true);
    }
  }

  function startEdit(recipient) {
    editingId = recipient.id;
    name.value = recipient.name;
    phone.value = recipient.phone_e164;
    checks.forEach((c) => {
      c.box.checked = (recipient.levels & c.bit) !== 0;
    });
    markClean(...fields);
    cancel.hidden = false;
    guard.update();
    renderStatic();
    name.focus();
  }

  function row(recipient) {
    const toggleBox = el("input", { type: "checkbox", "aria-label": t("recipients.enabled"), checked: recipient.enabled });
    toggleBox.addEventListener("change", () => toggle(recipient, toggleBox.checked));
    return el(
      "div",
      { class: "row" },
      el(
        "div",
        { class: "row-main" },
        el("div", { class: "row-title" }, recipient.name),
        el("div", { class: "muted number" }, maskNumber(recipient.phone_e164)),
        el(
          "div",
          { class: "chips" },
          ...LEVELS.filter((l) => (recipient.levels & l.bit) !== 0).map((l) => el("span", { class: `chip chip-${l.key}` }, t(`levels.${l.key}`))),
        ),
      ),
      el("label", { class: "toggle" }, toggleBox, el("span", {})),
      el("button", { class: "btn btn-ghost btn-small", type: "button", "aria-label": t("actions.edit"), onclick: () => startEdit(recipient) }, icon("edit")),
      el("button", { class: "btn btn-ghost btn-small", type: "button", "aria-label": t("actions.delete"), onclick: () => remove(recipient) }, icon("trash")),
    );
  }

  function renderStatic() {
    formTitle.textContent = t(editingId ? "recipients.edit" : "recipients.add");
    labels.name.textContent = t("recipients.name");
    labels.phone.textContent = t("recipients.phone");
    save.textContent = t("actions.save");
    cancel.textContent = t("actions.cancel");
    form.querySelectorAll("[data-level]").forEach((node) => {
      node.textContent = t(`levels.${node.dataset.level}`);
    });
    guard.update();
  }

  async function load() {
    try {
      recipients = await api("GET", "/recipients");
      pager.set(recipients);
    } catch (err) {
      toast(err.message, true);
    }
  }

  renderStatic();

  return {
    element,
    async show() {
      renderStatic();
      await load();
    },
    refreshLanguage() {
      renderStatic();
      pager.redraw();
    },
  };
}
