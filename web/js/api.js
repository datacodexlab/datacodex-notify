const API = "/api/v1/ui";
const CSRF_KEY = "dcn_csrf";

let csrfToken = readStoredToken();

function readStoredToken() {
  try {
    return sessionStorage.getItem(CSRF_KEY) || "";
  } catch {
    return "";
  }
}

function storeToken(token) {
  csrfToken = token;
  try {
    sessionStorage.setItem(CSRF_KEY, token);
  } catch {
    // Without storage the session lasts until the page reloads.
  }
}

export class ApiError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}

/**
 * Exchanges the one-time launch token from the URL for a session cookie and
 * removes it from the address bar. Returns true when a session is available.
 */
export async function startSession() {
  const params = new URLSearchParams(location.search);
  const launch = params.get("launch");
  if (!launch) {
    return csrfToken !== "";
  }
  history.replaceState(null, "", location.pathname);
  const res = await fetch(`${API}/session`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ launch_token: launch }),
  });
  if (!res.ok) {
    return csrfToken !== "";
  }
  const body = await res.json();
  storeToken(body.csrf_token);
  return true;
}

export async function api(method, path, body) {
  const headers = {};
  if (method !== "GET") {
    headers["X-CSRF-Token"] = csrfToken;
  }
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
  }
  const res = await fetch(`${API}${path}`, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  const data = text ? JSON.parse(text) : null;
  if (!res.ok) {
    throw new ApiError(res.status, (data && data.error) || res.statusText);
  }
  return data;
}

/** Like api() but returns the response body as a Blob (used for audio). */
export async function apiBlob(method, path) {
  const res = await fetch(`${API}${path}`, {
    method,
    headers: method === "GET" ? {} : { "X-CSRF-Token": csrfToken },
  });
  if (!res.ok) {
    const text = await res.text();
    let message = res.statusText;
    try {
      message = JSON.parse(text).error || message;
    } catch {
      // Keep the status text when the body is not JSON.
    }
    throw new ApiError(res.status, message);
  }
  return res.blob();
}

export const qrUrl = () => `${API}/qr?t=${Date.now()}`;
