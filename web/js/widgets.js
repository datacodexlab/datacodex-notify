import { el, isDirty, markClean } from "./dom.js";
import { t } from "./i18n.js";

/**
 * Sub-tabs inside a main tab. Each definition is {key, label, panel, show}: label
 * is a function returning the (translated) text, panel is the element shown for
 * the key, and show (optional) runs whenever the sub-tab opens.
 */
export function createSubtabs(root, defs, onSelect) {
  const bar = el("nav", { class: "subtabs", role: "tablist" });
  const buttons = new Map();
  for (const def of defs) {
    const button = el("button", { class: "subtab", role: "tab", type: "button", "data-sub": def.key, onclick: () => select(def.key) });
    buttons.set(def.key, button);
    bar.append(button);
  }
  const body = el("div", { class: "subpanels" }, ...defs.map((d) => d.panel));
  root.append(bar, body);

  let current = defs[0].key;

  function refreshLabels() {
    for (const def of defs) {
      buttons.get(def.key).textContent = def.label();
    }
  }

  function select(key, runShow = true) {
    current = key;
    for (const def of defs) {
      const active = def.key === key;
      buttons.get(def.key).classList.toggle("active", active);
      buttons.get(def.key).setAttribute("aria-selected", String(active));
      def.panel.hidden = !active;
    }
    const def = defs.find((d) => d.key === key);
    if (runShow && def.show) {
      def.show();
    }
    if (onSelect) {
      onSelect(key);
    }
  }

  refreshLabels();
  select(current, false);

  return {
    select,
    refreshLabels,
    get current() {
      return current;
    },
  };
}

/**
 * A paginated list in a fixed area. set(items) replaces the items and keeps the
 * current page when it still exists; render(item) returns the row element.
 */
export function createPager({ pageSize, render, empty }) {
  let items = [];
  let page = 0;

  const list = el("div", { class: "pager-list" });
  const prev = el("button", { class: "btn btn-small btn-ghost", type: "button", onclick: () => go(-1) });
  const next = el("button", { class: "btn btn-small btn-ghost", type: "button", onclick: () => go(1) });
  const label = el("span", { class: "muted number" });
  const bar = el("div", { class: "pager-bar" }, prev, label, next);
  const element = el("div", { class: "pager" }, list, bar);

  function pages() {
    return Math.max(1, Math.ceil(items.length / pageSize));
  }

  function go(delta) {
    page = Math.min(Math.max(page + delta, 0), pages() - 1);
    draw();
  }

  function draw() {
    page = Math.min(page, pages() - 1);
    const slice = items.slice(page * pageSize, (page + 1) * pageSize);
    if (items.length === 0) {
      list.replaceChildren(el("div", { class: "empty" }, typeof empty === "function" ? empty() : empty || ""));
    } else {
      list.replaceChildren(...slice.map(render));
    }
    bar.hidden = pages() <= 1;
    prev.disabled = page === 0;
    next.disabled = page >= pages() - 1;
    prev.textContent = "\u2039";
    next.textContent = "\u203A";
    prev.setAttribute("aria-label", t("common.prev_page"));
    next.setAttribute("aria-label", t("common.next_page"));
    label.textContent = `${page + 1} / ${pages()}`;
  }

  return {
    element,
    set(newItems) {
      items = newItems;
      draw();
    },
    redraw: draw,
    reset() {
      page = 0;
    },
  };
}

/**
 * Unsaved-change marker for one form: shows a small "unsaved" label while any of
 * its fields differs from what was last saved. clean() marks every field saved.
 */
export function createGuard(fields) {
  const marker = el("span", { class: "unsaved", hidden: true });
  function update() {
    marker.hidden = !fields.some(isDirty);
    marker.textContent = `\u25CF ${t("actions.unsaved")}`;
  }
  for (const field of fields) {
    field.addEventListener("input", update);
    field.addEventListener("change", update);
  }
  return {
    marker,
    update,
    clean() {
      markClean(...fields);
      update();
    },
  };
}

/** A collapsed section (details element) whose title is set with setTitle. */
export function createExpander(...children) {
  const summary = el("summary", {});
  const box = el("details", { class: "expander" }, summary, el("div", { class: "expander-body" }, ...children));
  return {
    element: box,
    setTitle(text) {
      summary.textContent = text;
    },
  };
}
