import { api } from "./api.js";
import { el, icon, toast } from "./dom.js";
import { language, t } from "./i18n.js";
import { createPager } from "./widgets.js";

const LEVELS = ["info", "warning", "critical"];
const STATUSES = ["queued", "sending", "sent", "delivered", "read", "failed"];
const PAGE_SIZE = 7;

export function statusClass(status) {
  if (status === "failed" || status === "voice_failed") {
    return "status-fail";
  }
  if (status === "queued" || status === "sending") {
    return "status-wait";
  }
  return "status-ok";
}

/** Delivery ticks: one for sent, two for delivered, two (blue) for read. */
export function ticks(status) {
  switch (status) {
    case "sent":
      return "\u2713";
    case "delivered":
      return "\u2713\u2713";
    case "read":
      return "\u2713\u2713";
    case "failed":
    case "voice_failed":
      return "\u2715";
    case "queued":
    case "sending":
      return "\u25F7";
    default:
      return "\u2022";
  }
}

/** The first line of a stored message (its header) without the WhatsApp bold marks. */
export function messageHeader(message) {
  const first = (message.text || "").split("\n")[0] || "";
  return first.replaceAll("*", "").trim() || message.source || "-";
}

export function formatClock(iso) {
  return new Date(iso).toLocaleTimeString(language() === "ar" ? "ar-EG" : "en-GB", { hour: "2-digit", minute: "2-digit" });
}

/** One compact message line: header, time, delivery ticks. */
export function messageLine(message, extra = []) {
  return el(
    "div",
    { class: "msg-line" },
    el("span", { class: "row-main row-title" }, messageHeader(message)),
    el("span", { class: "log-time" }, formatClock(message.created_at)),
    el("span", { class: `ticks ${statusClass(message.status)} ${message.status === "read" ? "ticks-read" : ""}`, title: t(`status.${message.status}`) }, ticks(message.status)),
    ...extra,
  );
}

/** The full message log: filters, a paginated list, retry, and expandable details. */
export function createLogPanel() {
  let messages = [];
  const expanded = new Set();

  const level = el("select", { "aria-label": "level", onchange: () => reload(true) });
  const status = el("select", { "aria-label": "status", onchange: () => reload(true) });
  const pager = createPager({ pageSize: PAGE_SIZE, render: row, empty: () => t("log.empty") });
  const element = el("div", { class: "log-panel" }, el("div", { class: "filters" }, level, status), pager.element);

  function fillFilter(select, allKey, keys, labelPrefix) {
    const previous = select.value;
    select.replaceChildren(
      el("option", { value: "" }, t(allKey)),
      ...keys.map((key) => el("option", { value: key }, t(`${labelPrefix}.${key}`))),
    );
    select.value = previous;
  }

  async function retry(message) {
    try {
      await api("POST", `/log/${message.id}/retry`);
      toast(t("log.retried"));
      await reload(false);
    } catch (err) {
      toast(err.message, true);
    }
  }

  function row(message) {
    const extra = [];
    if (message.status === "failed") {
      extra.push(
        el(
          "button",
          {
            class: "btn btn-small",
            type: "button",
            onclick: (event) => {
              event.stopPropagation();
              retry(message);
            },
          },
          icon("retry"),
          t("actions.retry"),
        ),
      );
    }
    const open = expanded.has(message.id);
    const children = [messageLine(message, extra)];
    if (open) {
      const lines = [message.text];
      if (message.last_error) {
        lines.push(`${t("log.error")}: ${message.last_error}`);
      }
      lines.push(`${t("log.attempts")}: ${message.attempts}`);
      children.push(el("div", { class: "log-details" }, lines.join("\n")));
    }
    return el(
      "div",
      {
        class: "row log-row",
        tabindex: "0",
        role: "button",
        "aria-expanded": String(open),
        onclick: () => toggleRow(message.id),
        onkeydown: (event) => {
          if (event.key === "Enter" || event.key === " ") {
            event.preventDefault();
            toggleRow(message.id);
          }
        },
      },
      ...children,
    );
  }

  function toggleRow(id) {
    if (!expanded.delete(id)) {
      expanded.add(id);
    }
    pager.redraw();
  }

  function render() {
    fillFilter(level, "log.all_levels", LEVELS, "levels");
    fillFilter(status, "log.all_statuses", STATUSES, "status");
    pager.set(messages);
  }

  async function reload(resetPage) {
    const query = new URLSearchParams();
    if (level.value) {
      query.set("level", level.value);
    }
    if (status.value) {
      query.set("status", status.value);
    }
    try {
      messages = await api("GET", `/log?${query}`);
      if (resetPage) {
        pager.reset();
      }
      render();
    } catch (err) {
      toast(err.message, true);
    }
  }

  return {
    element,
    show: () => reload(true),
    refresh: () => reload(false),
    refreshLanguage: render,
  };
}
