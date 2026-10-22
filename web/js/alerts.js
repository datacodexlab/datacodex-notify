import { api } from "./api.js";
import { el, icon, setIfClean, toast, trackEdits } from "./dom.js";
import { language, t } from "./i18n.js";
import { createGuard, createPager, createSubtabs } from "./widgets.js";

const PAUSE_MINUTES = 60;
const ALERT_MODES = ["text", "voice", "both"];
const HEARTBEATS_PER_PAGE = 3;

function numberField(id) {
  return el("input", { type: "number", id, min: "0", step: "1", inputmode: "numeric", dir: "ltr" });
}

function timeField(id) {
  return el("input", { type: "time", id, dir: "ltr" });
}

/**
 * The Alerts tab (pause, quiet hours, limits, app alerts, heartbeats). The form
 * fields keep the user's unsaved edits when the page refreshes itself: they are
 * rewritten from the server only when the tab opens and after a save.
 */
export function createAlertsView(root) {
  let data = null;
  let pending = { form: true, alerts: true };

  // ---- Quiet & pause
  const pauseTitle = el("h2", {});
  const pauseStatus = el("p", { class: "muted" });
  const pauseHour = el("button", { class: "btn btn-small", type: "button", onclick: () => pause(PAUSE_MINUTES) });
  const pauseForever = el("button", { class: "btn btn-small", type: "button", onclick: () => pause(0) });
  const resume = el("button", { class: "btn btn-small btn-primary", type: "button", onclick: onResume });
  const pauseCard = el("div", { class: "card" }, pauseTitle, pauseStatus, el("div", { class: "form-actions wrap" }, pauseHour, pauseForever, resume));

  const quietTitle = el("h2", {});
  const quietEnabled = el("input", { type: "checkbox", id: "quiet-enabled" });
  const quietEnabledLabel = el("label", { for: "quiet-enabled" });
  const quietStart = timeField("quiet-start");
  const quietEnd = timeField("quiet-end");
  const quietStartLabel = el("label", { for: "quiet-start" });
  const quietEndLabel = el("label", { for: "quiet-end" });
  const limitsTitle = el("h2", {});
  const dedupe = numberField("dedupe-minutes");
  const dedupeLabel = el("label", { for: "dedupe-minutes" });
  const rate = numberField("rate-limit");
  const rateLabel = el("label", { for: "rate-limit" });
  const saveQuiet = el("button", { class: "btn btn-primary", type: "button", onclick: onSave });

  const quietFields = [quietEnabled, quietStart, quietEnd, dedupe, rate];
  const quietGuard = createGuard(quietFields);
  const quietCard = el(
    "div",
    { class: "card" },
    quietTitle,
    el("div", { class: "checks field" }, el("label", {}, quietEnabled, quietEnabledLabel)),
    el("div", { class: "pair-fields" }, el("div", { class: "field" }, quietStartLabel, quietStart), el("div", { class: "field" }, quietEndLabel, quietEnd)),
    limitsTitle,
    el("div", { class: "pair-fields" }, el("div", { class: "field" }, dedupeLabel, dedupe), el("div", { class: "field" }, rateLabel, rate)),
    el("div", { class: "form-actions" }, saveQuiet, quietGuard.marker),
  );
  const quietPanel = el("div", { class: "subpanel" }, pauseCard, quietCard);

  // ---- App alerts
  const alertsHint = el("p", { class: "muted" });
  const alertsList = el("div", {});
  const alertsPanel = el("div", { class: "subpanel" }, el("div", { class: "card" }, alertsHint, alertsList));

  // ---- Heartbeat
  const hbEvery = numberField("hb-every");
  const hbEveryLabel = el("label", { for: "hb-every" });
  const hbGrace = numberField("hb-grace");
  const hbGraceLabel = el("label", { for: "hb-grace" });
  const hbTitle = el("h2", {});
  const saveHb = el("button", { class: "btn btn-primary", type: "button", onclick: onSave });
  const hbFields = [hbEvery, hbGrace];
  const hbGuard = createGuard(hbFields);
  const hbListTitle = el("h2", {});
  const hbPager = createPager({ pageSize: HEARTBEATS_PER_PAGE, render: heartbeatRow, empty: () => t("rules.hb_empty") });
  const hbPanel = el(
    "div",
    { class: "subpanel" },
    el(
      "div",
      { class: "card" },
      hbTitle,
      el("div", { class: "pair-fields" }, el("div", { class: "field" }, hbEveryLabel, hbEvery), el("div", { class: "field" }, hbGraceLabel, hbGrace)),
      el("div", { class: "form-actions" }, saveHb, hbGuard.marker),
    ),
    el("div", { class: "card fill-card" }, hbListTitle, hbPager.element),
  );

  const formFields = [...quietFields, ...hbFields];
  trackEdits(...formFields);

  const tabs = createSubtabs(root, [
    { key: "quiet", label: () => t("rules.sub_quiet"), panel: quietPanel, show: () => load({ form: true }) },
    { key: "alerts", label: () => t("rules.sub_alerts"), panel: alertsPanel, show: () => load({ alerts: true }) },
    { key: "heartbeat", label: () => t("rules.sub_heartbeat"), panel: hbPanel, show: () => load({ form: true }) },
  ]);

  async function load(parts = {}) {
    pending = { form: pending.form || Boolean(parts.form), alerts: pending.alerts || Boolean(parts.alerts) };
    try {
      const [rules, alerts] = await Promise.all([api("GET", "/rules"), api("GET", "/alerts")]);
      data = { ...rules, alerts: alerts.alerts };
      render();
    } catch (err) {
      toast(err.message, true);
    }
  }

  async function run(action, successKey, parts) {
    try {
      await action();
      if (successKey) {
        toast(t(successKey));
      }
      await load(parts);
    } catch (err) {
      toast(err.message, true, 5000);
    }
  }

  function pause(minutes) {
    run(() => api("POST", "/pause", { minutes }), "rules.paused_done");
  }

  function onResume() {
    run(() => api("DELETE", "/pause"), "rules.resumed_done");
  }

  function readNumber(input) {
    return input.value === "" ? -1 : Number(input.value);
  }

  function onSave() {
    quietGuard.clean();
    hbGuard.clean();
    run(
      () =>
        api("PUT", "/rules", {
          quiet_enabled: quietEnabled.checked,
          quiet_start: quietStart.value,
          quiet_end: quietEnd.value,
          dedupe_minutes: readNumber(dedupe),
          rate_limit_per_hour: readNumber(rate),
          heartbeat_interval_s: readNumber(hbEvery),
          heartbeat_grace_s: readNumber(hbGrace),
        }),
      "rules.saved",
      { form: true },
    );
  }

  function saveAlert(kind, enabled, mode) {
    run(() => api("PUT", "/alerts", { kind, enabled, mode }), undefined, { alerts: true });
  }

  function alertRow(row) {
    const id = `alert-${row.kind}`;
    const enabled = el("input", { type: "checkbox", id });
    enabled.checked = row.enabled;
    const mode = el("select", { id: `${id}-mode`, "aria-label": t(`rules.alert_${row.kind}`) });
    mode.replaceChildren(...ALERT_MODES.map((m) => el("option", { value: m }, t(`voice.mode_${m}`))));
    mode.value = row.mode;
    mode.disabled = !row.enabled;
    enabled.addEventListener("change", () => saveAlert(row.kind, enabled.checked, mode.value));
    mode.addEventListener("change", () => saveAlert(row.kind, enabled.checked, mode.value));
    return el("div", { class: "row" }, el("div", { class: "row-main checks" }, el("label", {}, enabled, t(`rules.alert_${row.kind}`))), mode);
  }

  function stopHeartbeat(hb) {
    if (!window.confirm(t("rules.hb_stop_confirm", { label: hb.label || hb.id }))) {
      return;
    }
    run(() => api("DELETE", `/heartbeats/${encodeURIComponent(hb.id)}`), "rules.hb_stopped");
  }

  function heartbeatRow(hb) {
    const when = new Date(hb.last_beat_at).toLocaleTimeString(language() === "ar" ? "ar-EG" : "en-GB", {
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
    return el(
      "div",
      { class: "row" },
      el(
        "div",
        { class: "row-main" },
        el("div", { class: "row-title" }, hb.label || hb.id),
        el("div", { class: "muted" }, `${hb.source || "-"} \u00B7 ${t("rules.hb_last")} ${when} \u00B7 ${t("rules.hb_every", { seconds: String(hb.interval_s) })}`),
      ),
      el("span", { class: `chip ${hb.state === "alerted" ? "chip-critical" : ""}` }, t(`rules.hb_state_${hb.state}`)),
      el("button", { class: "btn btn-ghost btn-small", type: "button", "aria-label": t("rules.hb_stop"), onclick: () => stopHeartbeat(hb) }, icon("x")),
    );
  }

  function renderStatic() {
    pauseTitle.textContent = t("rules.pause_title");
    pauseHour.textContent = t("rules.pause_hour");
    pauseForever.textContent = t("rules.pause_forever");
    resume.textContent = t("rules.resume");
    quietTitle.textContent = t("rules.quiet_title");
    quietEnabledLabel.textContent = t("rules.quiet_enabled");
    quietStartLabel.textContent = t("rules.quiet_start");
    quietEndLabel.textContent = t("rules.quiet_end");
    limitsTitle.textContent = t("rules.limits_title");
    dedupeLabel.textContent = t("rules.dedupe");
    rateLabel.textContent = t("rules.rate");
    hbTitle.textContent = t("rules.hb_defaults_title");
    hbEveryLabel.textContent = t("rules.hb_interval");
    hbGraceLabel.textContent = t("rules.hb_grace");
    hbListTitle.textContent = t("rules.hb_list_title");
    saveQuiet.textContent = t("actions.save");
    saveHb.textContent = t("actions.save");
    alertsHint.textContent = t("rules.alerts_hint");
    quietGuard.update();
    hbGuard.update();
    tabs.refreshLabels();
  }

  function pauseText(state) {
    if (!state.paused) {
      return t("rules.not_paused");
    }
    if (state.indefinite) {
      return t("rules.paused_forever");
    }
    const until = new Date(state.until).toLocaleTimeString(language() === "ar" ? "ar-EG" : "en-GB", { hour: "2-digit", minute: "2-digit" });
    return t("rules.paused_until", { time: until });
  }

  function render() {
    renderStatic();
    if (!data) {
      return;
    }
    const { settings, pause: pauseState, heartbeats, alerts } = data;
    if (pending.alerts) {
      alertsList.replaceChildren(...alerts.map(alertRow));
    }
    pauseStatus.textContent = pauseText(pauseState);
    resume.disabled = !pauseState.paused;
    if (pending.form) {
      setIfClean(quietEnabled, settings.quiet_enabled);
      setIfClean(quietStart, settings.quiet_start);
      setIfClean(quietEnd, settings.quiet_end);
      setIfClean(dedupe, settings.dedupe_minutes);
      setIfClean(rate, settings.rate_limit_per_hour);
      setIfClean(hbEvery, settings.heartbeat_interval_s);
      setIfClean(hbGrace, settings.heartbeat_grace_s);
    }
    pending = { form: false, alerts: false };
    hbPager.set(heartbeats);
  }

  return {
    show() {
      tabs.select(tabs.current);
    },
    refresh: () => load(),
    refreshLanguage() {
      if (data) {
        pending = { form: false, alerts: true };
        render();
      } else {
        renderStatic();
      }
    },
  };
}
