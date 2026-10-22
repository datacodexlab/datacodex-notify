const SVG_NS = "http://www.w3.org/2000/svg";

const ICONS = {
  retry: "M3 12a9 9 0 1 0 3-6.7M3 4v5h5",
  trash: "M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3",
  edit: "M4 20h4L19 9l-4-4L4 16zM14 6l4 4",
  check: "M5 12l5 5L20 7",
  x: "M6 6l12 12M18 6L6 18",
  clock: "M12 7v5l3 2M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0",
  back: "M15 6l-6 6 6 6",
  copy: "M9 9h10v10H9zM5 15V5h10",
};

export function icon(name) {
  const svg = document.createElementNS(SVG_NS, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("class", "icon");
  svg.setAttribute("aria-hidden", "true");
  const path = document.createElementNS(SVG_NS, "path");
  path.setAttribute("d", ICONS[name]);
  svg.append(path);
  return svg;
}

/** Creates an element; children may be nodes or strings (always inserted as text). */
export function el(tag, props = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props)) {
    if (key === "class") {
      node.className = value;
    } else if (key.startsWith("on")) {
      node.addEventListener(key.slice(2), value);
    } else if (value === true) {
      node.setAttribute(key, "");
    } else if (value !== false && value != null) {
      node.setAttribute(key, value);
    }
  }
  node.append(...children);
  return node;
}

/**
 * Unsaved-edit tracking. A refresh from the server must never overwrite a field
 * the user changed and has not saved: mark such fields with trackEdits, write
 * server values with setIfClean, and call markClean after a successful save.
 */
export function trackEdits(...nodes) {
  for (const node of nodes) {
    const mark = () => {
      node.dataset.dirty = "1";
    };
    node.addEventListener("input", mark);
    node.addEventListener("change", mark);
  }
}

export function isDirty(node) {
  return node.dataset.dirty === "1";
}

export function markClean(...nodes) {
  for (const node of nodes) {
    delete node.dataset.dirty;
  }
}

/** Writes a server value into a field unless the user has an unsaved edit there. */
export function setIfClean(node, value) {
  if (isDirty(node)) {
    return;
  }
  if (node.type === "checkbox") {
    node.checked = Boolean(value);
  } else {
    node.value = value;
  }
}

export function clear(node) {
  node.replaceChildren();
}

let toastTimer;

export function toast(message, isError = false, durationMs = 3000) {
  const node = document.getElementById("toast");
  node.textContent = message;
  node.className = isError ? "toast error" : "toast";
  node.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    node.hidden = true;
  }, durationMs);
}
