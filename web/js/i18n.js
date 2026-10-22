const bundles = {};
let current = "ar";

async function load(lang) {
  if (!bundles[lang]) {
    const res = await fetch(`i18n/${lang}.json`);
    bundles[lang] = await res.json();
  }
  return bundles[lang];
}

function lookup(bundle, key) {
  return key.split(".").reduce((node, part) => (node ? node[part] : undefined), bundle);
}

export function t(key, vars) {
  let text = lookup(bundles[current], key) ?? lookup(bundles.en || {}, key) ?? key;
  for (const [name, value] of Object.entries(vars || {})) {
    text = text.replaceAll(`{${name}}`, value);
  }
  return text;
}

export function language() {
  return current;
}

export async function setLanguage(lang) {
  await load("en");
  await load(lang);
  current = lang;
  document.documentElement.lang = lang;
  document.documentElement.dir = lang === "ar" ? "rtl" : "ltr";
  for (const node of document.querySelectorAll("[data-i18n]")) {
    node.textContent = t(node.dataset.i18n);
  }
}
