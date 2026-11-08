(() => {
  "use strict";

  const MOD_NAME = "Grim Poster";
  const MOD_VERSION = "0.2.0";
  const CONFIG_KEY = "botc-grim-poster-config-v1";
  const DB_NAME = "botc-grim-poster";
  const DB_STORE = "captures";
  const PENDING_KEY = "pending";
  const DISCORD_FILE_LIMIT = 9.5 * 1024 * 1024;
  const UPLOAD_RELAY_URL = "https://updates.themavryan.com/api/grim-poster/upload";

  if (globalThis.__BOTC_GRIM_POSTER__) return;
  globalThis.__BOTC_GRIM_POSTER__ = { version: MOD_VERSION };

  const state = {
    pending: null,
    capturing: false,
    capturePromise: null,
    stagePromise: null,
    staged: null,
    revealReadyPromise: null,
    selectedWinner: null,
    uploading: false,
    game2Proof: null,
    game2PromptCycle: null,
    game2Sending: false,
    revealPromptActive: false,
    revealActive: false,
    revealCycle: 0,
    replayingRevealClick: false,
    suppressObserver: false,
    config: null,
    observer: null,
    ui: {},
  };

  const isExtension = () =>
    Boolean(
      globalThis.chrome?.runtime?.id && globalThis.chrome?.storage?.local,
    );

  const isDesktopApp = () =>
    !isExtension() && Boolean(globalThis.__TAURI__?.core?.invoke);

  const defaultConfig = () => ({
    enabled: true,
    privacyFilter: true,
  });

  function cleanConfig(value) {
    const config = { ...defaultConfig(), ...(value || {}) };
    delete config.webhookUrl;
    delete config.username;
    // Remove legacy per-client notification secrets from stored settings.
    delete config.notifyGame2;
    delete config.revealSubmitKey;
    delete config.autoPost;
    return config;
  }

  async function getConfig() {
    try {
      if (isExtension()) {
        const result = await chrome.storage.local.get(CONFIG_KEY);
        const stored = result[CONFIG_KEY] || {};
        const clean = cleanConfig(stored);
        if ("webhookUrl" in stored || "username" in stored || "notifyGame2" in stored || "revealSubmitKey" in stored) {
          await chrome.storage.local.set({ [CONFIG_KEY]: clean });
        }
        return clean;
      }
      const stored = JSON.parse(localStorage.getItem(CONFIG_KEY) || "{}");
      const clean = cleanConfig(stored);
      if ("webhookUrl" in stored || "username" in stored || "notifyGame2" in stored || "revealSubmitKey" in stored) {
        localStorage.setItem(CONFIG_KEY, JSON.stringify(clean));
      }
      return clean;
    } catch {
      return defaultConfig();
    }
  }

  async function saveConfig(config) {
    const value = cleanConfig(config);
    if (isExtension()) {
      await chrome.storage.local.set({ [CONFIG_KEY]: value });
    } else {
      localStorage.setItem(CONFIG_KEY, JSON.stringify(value));
    }
    return value;
  }

  function openDatabase() {
    return new Promise((resolve, reject) => {
      const request = indexedDB.open(DB_NAME, 1);
      request.onupgradeneeded = () => {
        const database = request.result;
        if (!database.objectStoreNames.contains(DB_STORE)) {
          database.createObjectStore(DB_STORE);
        }
      };
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error);
    });
  }

  async function databaseAction(mode, action) {
    const database = await openDatabase();
    try {
      return await new Promise((resolve, reject) => {
        const transaction = database.transaction(DB_STORE, mode);
        const store = transaction.objectStore(DB_STORE);
        const request = action(store);
        request.onsuccess = () => resolve(request.result);
        request.onerror = () => reject(request.error);
        transaction.onerror = () => reject(transaction.error);
      });
    } finally {
      database.close();
    }
  }

  async function loadPending() {
    try {
      state.pending =
        (await databaseAction("readonly", (store) => store.get(PENDING_KEY))) ||
        null;
    } catch {
      state.pending = null;
    }
    updateStatusButton();
    return state.pending;
  }

  async function persistPending(capture) {
    state.pending = capture;
    try {
      await databaseAction("readwrite", (store) =>
        store.put(capture, PENDING_KEY),
      );
    } catch {
    }
    updateStatusButton();
  }

  async function clearPending() {
    state.pending = null;
    try {
      await databaseAction("readwrite", (store) => store.delete(PENDING_KEY));
    } catch {
    }
    updateStatusButton();
  }

  function injectStyles() {
    if (document.getElementById("gp-styles")) return;
    const style = document.createElement("style");
    style.id = "gp-styles";
    style.textContent = `
      #gp-launch {
        position: fixed; left: 12px; bottom: 12px; z-index: 9990;
        width: 38px; height: 38px; border: 2px solid #000; border-radius: 50%;
        color: #fff; background: rgba(0, 0, 0, .82); cursor: pointer;
        font: 700 14px/34px Arial, sans-serif; text-align: center;
        box-shadow: 0 2px 10px rgba(0, 0, 0, .65);
      }
      #gp-launch:hover { color: #ffad00; transform: scale(1.05); }
      #gp-launch.gp-pending::after, #gp-launch.gp-error::after {
        content: ""; position: absolute; width: 10px; height: 10px;
        top: -1px; right: -1px; border: 1px solid #000; border-radius: 50%;
        background: #ffad00;
      }
      #gp-launch.gp-error::after { background: #e44141; }
      #gp-modal, #gp-capture-screen {
        position: fixed; inset: 0; z-index: 1000000; display: none;
        align-items: center; justify-content: center;
        color: #fff; background: rgba(0, 0, 0, .76);
        font-family: "Trade Gothic LT Std", Arial, sans-serif;
      }
      #gp-modal.gp-open, #gp-capture-screen.gp-open { display: flex; }
      #gp-capture-screen {
        inset: 14px 12px auto; background: transparent; pointer-events: none;
      }
      #gp-panel {
        box-sizing: border-box; width: min(560px, calc(100vw - 32px));
        border: 3px solid #000; border-radius: 12px; padding: 18px;
        background: #17131c; box-shadow: 0 10px 50px #000;
      }
      #gp-panel h2 { margin: 0 0 4px; color: #ffad00; font-family: Dumbledor, serif; }
      #gp-panel p { margin: 6px 0 14px; line-height: 1.3; }
      #gp-panel label { display: block; margin: 12px 0 5px; font-weight: 700; }
      #gp-panel input[type="password"], #gp-panel input[type="text"] {
        box-sizing: border-box; width: 100%; border: 1px solid #777;
        border-radius: 6px; padding: 9px 10px; color: #fff; background: #09080b;
      }
      #gp-panel .gp-hint { font-size:12px; opacity:.85; }
      #gp-panel label.gp-check {
        display: grid !important; grid-template-columns: 18px minmax(0, 1fr);
        justify-content: start !important; justify-items: start; align-items: start;
        width: 100%; gap: 8px; text-align: left !important; font-weight: 400;
      }
      #gp-panel label.gp-check input { margin: 3px 0 0; }
      #gp-panel label.gp-check span { display: block; text-align: left !important; }
      #gp-panel .gp-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 16px; }
      #gp-panel button {
        border: 2px solid #000; border-radius: 7px; padding: 7px 12px;
        color: #fff; background: #4f216c; cursor: pointer; font: inherit;
      }
      #gp-panel button:hover { color: #ffad00; }
      #gp-panel button.gp-secondary { background: #29242e; }
      #gp-panel button:disabled { color: #888; cursor: not-allowed; }
      #gp-pending-message { min-height: 1.3em; color: #ffad00; }
      #gp-toast-container {
        position: fixed; left: 50%; bottom: 22px; z-index: 1000001;
        transform: translateX(-50%); pointer-events: none;
      }
      .gp-toast {
        min-width: 280px; max-width: min(640px, 90vw); margin-top: 8px;
        border: 2px solid #000; border-radius: 8px; padding: 9px 13px;
        color: #fff; background: rgba(20, 16, 24, .96); text-align: center;
        box-shadow: 0 4px 18px #000; opacity: 0; transform: translateY(8px);
        transition: opacity .2s, transform .2s;
      }
      .gp-toast.gp-visible { opacity: 1; transform: translateY(0); }
      .gp-toast.gp-success { border-color: #4a9f62; }
      .gp-toast.gp-error { border-color: #c94545; }
      #gp-capture-card {
        display: flex; align-items: center; gap: 10px;
        border: 2px solid #000; border-radius: 9px; padding: 8px 14px;
        background: rgba(23, 19, 28, .96); box-shadow: 0 4px 18px #000;
        text-align: left;
      }
      #gp-spinner {
        width: 18px; height: 18px; flex: 0 0 18px;
        border: 3px solid #555; border-top-color: #ffad00; border-radius: 50%;
        animation: gp-spin .8s linear infinite;
      }
      @keyframes gp-spin { to { transform: rotate(360deg); } }
      #gp-game2-prompt {
        position:fixed; bottom:17px; right:17px; z-index:1000002;
        display:flex; align-items:stretch; gap:7px;
        font:600 13px/1.3 Arial,sans-serif;
        filter:drop-shadow(0 3px 8px #000);
      }
      #gp-game2-prompt button { border:1px solid #a56ac1; border-radius:8px;
        padding:10px 12px; cursor:pointer; color:#fff; background:#653082; }
      #gp-game2-prompt button:hover { background:#7b399d; }
      #gp-game2-prompt button:disabled { opacity:.55;cursor:wait; }
      #gp-game2-dismiss { background:#24202a !important; padding:10px !important; }
      #gp-game2-override { position:fixed; inset:0; z-index:2147483646;
        display:flex; align-items:center; justify-content:center;
        background:rgba(0,0,0,.72); padding:24px; }
      #gp-game2-override-panel { box-sizing:border-box; width:min(450px,100%);
        padding:22px; border:1px solid #a56ac1; border-radius:12px;
        background:#231b29; color:#fff; box-shadow:0 16px 50px rgba(0,0,0,.65); }
      #gp-game2-override-panel h2 { font-size:18px; font-weight:700; margin:0 0 12px; }
      #gp-game2-override-panel p { margin:0 0 16px; line-height:1.5; }
      #gp-game2-override-actions { display:flex; justify-content:flex-end; gap:10px; }
      #gp-game2-override-actions button { border:1px solid #a56ac1; border-radius:7px;
        padding:10px 20px; background:#522567; color:white; cursor:pointer; font-weight:600; }
      #gp-game2-override-actions button:focus-visible { outline:3px solid #e6a8ff; }
      #gp-game2-override-actions #gp-game2-override-no { background:#24202a; }
      body.gp-capturing #gp-launch, body.gp-capturing #gp-modal,
      body.gp-capturing #gp-toast-container, body.gp-capturing #gp-game2-prompt, body.gp-capturing #gp-game2-override { visibility: hidden !important; }
    `;
    document.head.appendChild(style);
  }

  function createInterface() {
    if (document.getElementById("gp-launch")) return;

    const launcher = document.createElement("button");
    launcher.id = "gp-launch";
    launcher.className = "gp-no-capture";
    launcher.type = "button";
    launcher.textContent = "GP";
    launcher.title = `${MOD_NAME} settings`;

    const modal = document.createElement("div");
    modal.id = "gp-modal";
    modal.className = "gp-no-capture";
    modal.innerHTML = `
      <div id="gp-panel" role="dialog" aria-modal="true" aria-labelledby="gp-title">
        <h2 id="gp-title">Grim Poster</h2>
        <p>Capture the final private Grimoire and post it when the reveal closes.</p>
        <p>Grimoires are sent to the Blood on the Sharktower results channel.</p>
        <label class="gp-check"><input id="gp-enabled" type="checkbox" checked><span>Enable Grim Poster</span></label>
        <label class="gp-check"><input id="gp-privacy-filter" type="checkbox" checked><span>Hide player cameras in screenshots</span></label>
        <p id="gp-pending-message"></p>
        <div class="gp-actions">
          <button id="gp-save" type="button">Save</button>
          <button id="gp-test" type="button" class="gp-secondary">Test Discord connection</button>
          <button id="gp-send" type="button" class="gp-secondary">Send pending</button>
          <button id="gp-download" type="button" class="gp-secondary">Download pending</button>
          <button id="gp-discard" type="button" class="gp-secondary">Discard pending</button>
          <button id="gp-close" type="button" class="gp-secondary">Close</button>
        </div>
      </div>
    `;

    const toastContainer = document.createElement("div");
    toastContainer.id = "gp-toast-container";
    toastContainer.className = "gp-no-capture";

    const captureScreen = document.createElement("div");
    captureScreen.id = "gp-capture-screen";
    captureScreen.innerHTML = `
      <div id="gp-capture-card">
        <div id="gp-spinner"></div>
        <strong>Capturing Grimoire</strong>
      </div>
    `;

    document.body.append(launcher, modal, toastContainer, captureScreen);

    state.ui = {
      launcher,
      modal,
      enabled: modal.querySelector("#gp-enabled"),
      privacyFilter: modal.querySelector("#gp-privacy-filter"),
      pendingMessage: modal.querySelector("#gp-pending-message"),
      save: modal.querySelector("#gp-save"),
      test: modal.querySelector("#gp-test"),
      send: modal.querySelector("#gp-send"),
      download: modal.querySelector("#gp-download"),
      discard: modal.querySelector("#gp-discard"),
      close: modal.querySelector("#gp-close"),
      toastContainer,
      captureScreen,
    };

    launcher.addEventListener("click", openSettings);
    state.ui.close.addEventListener("click", closeSettings);
    modal.addEventListener("click", (event) => {
      if (event.target === modal) closeSettings();
    });
    state.ui.save.addEventListener("click", saveSettings);
    state.ui.test.addEventListener("click", testConnection);
    state.ui.send.addEventListener("click", () => uploadPending(true));
    state.ui.download.addEventListener("click", downloadPending);
    state.ui.discard.addEventListener("click", async () => {
      if (!state.config?.enabled) return;
      await clearPending();
      updatePendingControls();
      toast("Pending Grimoire discarded.");
    });
    document.addEventListener(
      "keydown",
      (event) => {
        if (event.key === "Escape" && modal.classList.contains("gp-open")) {
          closeSettings();
        }
        if (event.key === "Escape" && state.revealPromptActive) {
          discardPreparedReveal(state.revealCycle);
        }
        if (
          event.ctrlKey &&
          event.shiftKey &&
          event.key.toLowerCase() === "g"
        ) {
          event.preventDefault();
          openSettings();
        }
      },
      true,
    );
    updateStatusButton();
  }

  async function openSettings() {
    const config = await getConfig();
    state.config = config;
    state.ui.enabled.checked = config.enabled;
    state.ui.privacyFilter.checked = config.privacyFilter;
    updatePendingControls();
    state.ui.modal.classList.add("gp-open");
    state.ui.enabled.focus();
  }

  function closeSettings() {
    state.ui.modal?.classList.remove("gp-open");
  }

  function currentFormConfig() {
    return {
      enabled: state.ui.enabled.checked,
      privacyFilter: state.ui.privacyFilter.checked,
    };
  }

  async function saveSettings() {
    const config = currentFormConfig();
    await saveConfig(config);
    state.config = config;
    updatePendingControls();
    toast("Grim Poster settings saved.", "success");
    return true;
  }

  function updateStatusButton(error = false) {
    const launcher = state.ui.launcher;
    if (!launcher) return;
    launcher.classList.toggle("gp-pending", Boolean(state.pending) && !error);
    launcher.classList.toggle("gp-error", error);
    launcher.title = state.pending
      ? `${MOD_NAME}: a Grimoire is waiting to be sent`
      : `${MOD_NAME} settings`;
  }

  function updatePendingControls() {
    if (!state.ui.pendingMessage) return;
    const disabled = !state.config?.enabled;
    if (state.pending) {
      const winner =
        state.pending.winner === "Pending"
          ? "Winner selection pending"
          : `${state.pending.winner || "Unknown"} victory`;
      const date = new Date(state.pending.capturedAt).toLocaleString();
      state.ui.pendingMessage.textContent = `Pending: ${winner} — ${date}`;
    } else {
      state.ui.pendingMessage.textContent =
        "No Grimoire is waiting to be sent.";
    }
    for (const control of [
      state.ui.send,
      state.ui.download,
      state.ui.discard,
    ]) {
      if (control)
        control.disabled = !state.pending || state.uploading || disabled;
    }
    state.ui.test.disabled = disabled || state.uploading;
  }

  function toast(message, type = "info", duration = 5000) {
    if (!state.ui.toastContainer) return;
    const item = document.createElement("div");
    item.className = `gp-toast gp-${type}`;
    item.textContent = message;
    state.ui.toastContainer.appendChild(item);
    requestAnimationFrame(() => item.classList.add("gp-visible"));
    setTimeout(() => {
      item.classList.remove("gp-visible");
      setTimeout(() => item.remove(), 250);
    }, duration);
  }

  function nextFrame() {
    return new Promise((resolve) => requestAnimationFrame(resolve));
  }

  function setCaptureScreen(visible) {
    state.ui.captureScreen?.classList.toggle("gp-open", visible);
  }

  function revealIsActive() {
    return Boolean(
      document.querySelector("#app.hook-reveal-grimoire") ||
      document.querySelector("#grimoire.reveal #reveal"),
    );
  }

  function findRevealPrompt() {
    for (const group of document.querySelectorAll(
      ".modal-backdrop.dialogue .button-group, .dialogue .button-group",
    )) {
      const buttons = [...group.children].filter((element) =>
        element.matches(".button"),
      );
      if (buttons.length === 3) return { group, buttons };
    }
    return null;
  }

  function detectWinner() {
    return document.querySelector("#reveal .result.evil") ? "Evil" : "Good";
  }

  function cleanScriptName(value) {
    return String(value || "")
      .replace(/\s+/g, " ")
      .trim()
      .replace(/\s+by\s+$/i, "");
  }

  function activeEdition() {
    const appElement = document.querySelector("#app");
    const vueApp = appElement?.__vue_app__;
    const root = vueApp?._instance;
    const proxy = root?.proxy;
    const townInfoProxy = document.querySelector(".town-info")
      ?.__vueParentComponent?.proxy;
    const store =
      proxy?.$store ||
      vueApp?._context?.provides?.store ||
      appElement?.__vueParentComponent?.proxy?.$store ||
      townInfoProxy?.$store;
    let edition = store?.state?.edition;
    if (!edition?.id) edition = store?.getters?.editionExport?.edition;
    if (!edition?.id) edition = proxy?.edition || townInfoProxy?.edition;
    if (!edition?.id) {
      try {
        edition = JSON.parse(localStorage.getItem("edition") || "null")?.edition;
      } catch {
        edition = null;
      }
    }
    return { edition, proxies: [proxy, townInfoProxy], store };
  }

  function detectScriptName() {
    const visibleName = cleanScriptName(
      document.querySelector(".town-info .edition")?.textContent,
    );
    if (visibleName) return visibleName;

    const { edition, proxies, store } = activeEdition();
    if (!edition) return "Unknown script";

    const customName = cleanScriptName(edition.name);
    if (customName) {
      const author = cleanScriptName(edition.author);
      return author && !customName.toLowerCase().endsWith(`by ${author}`.toLowerCase())
        ? `${customName} by ${author}`
        : customName;
    }

    const id = cleanScriptName(edition.id);
    if (id) {
      const availableEdition = store?.state?.editionsAvailable?.get?.(id);
      const availableName = cleanScriptName(availableEdition?.name);
      if (availableName) return availableName;

      for (const proxy of proxies) {
        if (typeof proxy?.$t !== "function") continue;
        const translated = cleanScriptName(proxy.$t(`editions.${id}.name`));
        if (translated && translated !== `editions.${id}.name`) return translated;
      }

      const knownNames = {
        tb: "Trouble Brewing",
        snv: "Sects & Violets",
        bmr: "Bad Moon Rising",
      };
      if (knownNames[id.toLowerCase()]) return knownNames[id.toLowerCase()];
      return id.replace(/[-_]+/g, " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
    }
    return "Unknown script";
  }

  function saveClassState(element) {
    return element ? element.getAttribute("class") : null;
  }

  function restoreClassState(element, value) {
    if (!element) return;
    if (value === null) element.removeAttribute("class");
    else element.setAttribute("class", value);
  }

  function addCaptureDefinitions(grimoire) {
    const definitions = document.querySelector("#gradients");
    if (!definitions || grimoire.contains(definitions)) return null;
    const copy = definitions.cloneNode(true);
    copy.id = "gp-capture-definitions";
    copy.setAttribute("aria-hidden", "true");
    grimoire.prepend(copy);
    return copy;
  }

  function isLiveCameraVideo(video) {
    const stream = video?.srcObject;
    if (!stream || typeof stream.getVideoTracks !== "function") return false;
    return stream.getVideoTracks().some((track) => track.readyState !== "ended");
  }

  function blobToDataUrl(blob) {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(reader.result);
      reader.onerror = () => reject(reader.error || new Error("Image read failed"));
      reader.readAsDataURL(blob);
    });
  }

  async function screenshotBlobToCanvas(blob) {
    const bitmap = await createImageBitmap(blob);
    const canvas = document.createElement("canvas");
    canvas.width = bitmap.width;
    canvas.height = bitmap.height;
    canvas.getContext("2d", { alpha: false }).drawImage(bitmap, 0, 0);
    bitmap.close?.();
    return canvas;
  }

  async function fetchProfileImageDataUrl(url) {
    try {
      const response = await fetch(url, { credentials: "omit" });
      if (!response.ok) throw new Error(`Profile image returned HTTP ${response.status}`);
      const blob = await response.blob();
      if (!blob.type.startsWith("image/"))
        throw new Error("Profile image was not an image");
      return await blobToDataUrl(blob);
    } catch (error) {
      if (isExtension()) {
        const result = await chrome.runtime.sendMessage({
          type: "grim-poster-avatar",
          url,
        });
        if (result?.ok && result.dataUrl?.startsWith("data:image/"))
          return result.dataUrl;
      }
      throw error;
    }
  }

  async function inlineProfileImages(grimoire) {
    const restorations = [];
    for (const avatar of grimoire.querySelectorAll(".player-avatar")) {
      const originalStyle = avatar.getAttribute("style");
      let backgroundImage = getComputedStyle(avatar).backgroundImage;
      const urls = [...backgroundImage.matchAll(/url\(["']?([^"')]+)["']?\)/g)];
      let changed = false;
      for (const match of urls) {
        let imageUrl;
        try {
          imageUrl = new URL(match[1], document.baseURI);
        } catch {
          continue;
        }
        if (
          imageUrl.protocol !== "https:" ||
          imageUrl.hostname !== "img.botc.app" ||
          !imageUrl.pathname.startsWith("/avatars/")
        ) {
          continue;
        }
        try {
          const dataUrl = await fetchProfileImageDataUrl(imageUrl.href);
          backgroundImage = backgroundImage.replace(match[0], `url("${dataUrl}")`);
          changed = true;
        } catch (error) {
          console.warn(`${MOD_NAME}: profile image could not be embedded`, error);
        }
      }
      if (changed) {
        restorations.push({ avatar, originalStyle });
        avatar.style.backgroundImage = backgroundImage;
      }
    }
    return restorations;
  }

  async function captureWithWindowsCompanion() {
    let response;
    try {
      response = await fetch("http://127.0.0.1:47831/capture", {
        method: "POST",
        mode: "cors",
        credentials: "omit",
        cache: "no-store",
        headers: { "Content-Type": "text/plain" },
        body: "capture",
      });
    } catch (error) {
      throw new Error(
        "Could not reach the Windows companion. Start BOTC with its companion launcher and retry.",
        { cause: error },
      );
    }
    if (!response.ok) {
      const detail = (await response.text()).trim();
      throw new Error(detail || `Windows companion returned HTTP ${response.status}`);
    }
    const image = await response.blob();
    if (image.type !== "image/png" || image.size < 8)
      throw new Error("Windows companion returned an invalid screenshot.");
    return image;
  }

  function addVideoRedactions(grimoire) {
    const redactions = [];
    for (const video of grimoire.querySelectorAll("video")) {
      if (!isLiveCameraVideo(video)) continue;
      const style = getComputedStyle(video);
      if (
        style.display === "none" ||
        style.visibility === "hidden" ||
        style.opacity === "0"
      ) {
        continue;
      }

      const visibility = video.style.visibility;
      video.style.visibility = "hidden";
      redactions.push({ video, visibility });
    }
    return redactions;
  }

  function addNameplateCaptureStyles(grimoire) {
    const style = document.createElement("style");
    style.id = "gp-capture-nameplate-style";
    style.textContent = `
      .gp-capture-nameplate::before,
      .gp-capture-nameplate::after {
        display: none !important;
        content: none !important;
        background: none !important;
      }
    `;
    document.head.appendChild(style);

    const states = [...grimoire.querySelectorAll(".nameplate")].map(
      (element) => {
      const state = {
        element,
        style: element.getAttribute("style"),
        className: element.getAttribute("class"),
      };
      for (const className of [...element.classList]) {
        if (
          // BOTC now adds cosmetic level/nameplate classes dynamically.
          // Strip all visual variants for capture, not only older role borders.
          className.startsWith("level-") ||
          className.startsWith("border-")
        ) {
          element.classList.remove(className);
        }
      }
      element.classList.add("gp-capture-nameplate");
      element.style.setProperty("background", "#0009", "important");
      element.style.setProperty("background-image", "none", "important");
      element.style.setProperty("background-color", "#0009", "important");
      return state;
      },
    );
    return { states, style };
  }

  function restoreNameplateCaptureStyles(captureStyles) {
    if (!captureStyles) return;
    captureStyles.style.remove();
    captureStyles.states.forEach(({ element, style, className }) => {
      if (style === null) element.removeAttribute("style");
      else element.setAttribute("style", style);
      if (className === null) element.removeAttribute("class");
      else element.setAttribute("class", className);
    });
  }

  function canvasBlob(canvas, quality) {
    return new Promise((resolve, reject) => {
      canvas.toBlob(
        (blob) =>
          blob ? resolve(blob) : reject(new Error("Image encoding failed")),
        "image/jpeg",
        quality,
      );
    });
  }

  async function encodeForDiscord(canvas) {
    let blob = await canvasBlob(canvas, 0.92);
    if (blob.size <= DISCORD_FILE_LIMIT) return blob;
    blob = await canvasBlob(canvas, 0.76);
    if (blob.size <= DISCORD_FILE_LIMIT) return blob;

    const scale = Math.max(
      0.5,
      Math.min(0.9, Math.sqrt(DISCORD_FILE_LIMIT / blob.size) * 0.9),
    );
    const resized = document.createElement("canvas");
    resized.width = Math.max(1, Math.round(canvas.width * scale));
    resized.height = Math.max(1, Math.round(canvas.height * scale));
    resized
      .getContext("2d", { alpha: false })
      .drawImage(canvas, 0, 0, resized.width, resized.height);
    return canvasBlob(resized, 0.8);
  }

  async function captureFinalGrimoire() {
    if (state.capturing) return state.capturePromise;
    const grimoire = document.querySelector("#grimoire");
    const app = document.querySelector("#app");
    if (!grimoire || !app) return null;
    if (!isDesktopApp() && !globalThis.htmlToImage?.toCanvas) {
      throw new Error("The screenshot library did not load");
    }

    const cycle = state.revealCycle;
    state.capturing = true;
    // The snapshot is immutable once the Windows companion has returned its PNG.
    // Unblock BOTC at that point rather than waiting for JPEG conversion and
    // IndexedDB persistence. Those still finish before posting to Discord.
    let resolveRevealReady;
    state.revealReadyPromise = new Promise((resolve) => {
      resolveRevealReady = resolve;
    });
    state.capturePromise = (async () => {
      setCaptureScreen(true);
      document.body.classList.add("gp-capturing");
      state.suppressObserver = true;

      const appClass = saveClassState(app);
      const grimoireClass = saveClassState(grimoire);
      let captureDefinitions = null;
      let videoRedactions = [];
      let avatarImageRestorations = [];
      let nameplateStyles = null;
      let hiddenNodes = [];
      let canvas;
      let screenshot;
      let capturedAt;
      let winner;
      let scriptName;

      try {
        captureDefinitions = addCaptureDefinitions(grimoire);
        nameplateStyles = addNameplateCaptureStyles(grimoire);
        if (!isDesktopApp())
          avatarImageRestorations = await inlineProfileImages(grimoire);
        if (state.config?.privacyFilter !== false)
          videoRedactions = addVideoRedactions(grimoire);
        const prompt = findRevealPrompt();
        const promptContainer = prompt?.group.closest(".dialogue");
        hiddenNodes = [
          ...new Set([
            ...document.querySelectorAll(
              "#reveal, .modal-backdrop, .gp-no-capture",
            ),
            ...(prompt ? [prompt.group] : []),
            ...(promptContainer ? [promptContainer] : []),
          ]),
        ].map((element) => ({
          element,
          style: element.getAttribute("style"),
        }));
        hiddenNodes.forEach(({ element }) => {
          element.style.setProperty("display", "none", "important");
          element.style.setProperty("visibility", "hidden", "important");
        });

        // WebView2 uses a native screenshot; waiting for all web fonts here
        // can stall the reveal even though the existing Grim is already drawn.
        if (!isDesktopApp()) {
          if (document.fonts?.ready) await document.fonts.ready;
          await nextFrame();
          await nextFrame();
        }

        app.classList.remove("hook-reveal-grimoire");
        for (const name of [
          "reveal",
          "townsquare",
          "vote",
          "pointing",
          "gather",
          "rules",
          "firstnight",
        ]) {
          grimoire.classList.remove(name);
        }

        const pixelRatio = Math.min(window.devicePixelRatio || 1, 1.5);
        if (isDesktopApp()) {
          // Give WebView2 two frames to paint the unobstructed Grimoire.
          await nextFrame();
          await nextFrame();
          screenshot = await captureWithWindowsCompanion();
        } else {
          canvas = await htmlToImage.toCanvas(grimoire, {
            pixelRatio,
            cacheBust: false,
            skipAutoScale: true,
            backgroundColor: "#111",
            filter: (node) =>
              !(
                node?.classList?.contains("gp-no-capture") ||
                node?.id === "reveal" ||
                (node?.tagName === "VIDEO" &&
                  state.config?.privacyFilter !== false &&
                  isLiveCameraVideo(node)) ||
                node?.tagName === "IFRAME"
              ),
          });
        }
        capturedAt = Date.now();
        winner = state.selectedWinner || (document.querySelector("#reveal")
          ? detectWinner()
          : "Pending");
        scriptName = detectScriptName();
      } finally {
        restoreClassState(app, appClass);
        restoreClassState(grimoire, grimoireClass);
        hiddenNodes.forEach(({ element, style }) => {
          if (style === null) element.removeAttribute("style");
          else element.setAttribute("style", style);
        });
        captureDefinitions?.remove();
        avatarImageRestorations.forEach(({ avatar, originalStyle }) => {
          if (originalStyle === null) avatar.removeAttribute("style");
          else avatar.setAttribute("style", originalStyle);
        });
        videoRedactions.forEach(({ video, visibility }) => {
          video.style.visibility = visibility;
        });
        restoreNameplateCaptureStyles(nameplateStyles);
        state.suppressObserver = false;
        document.body.classList.remove("gp-capturing");
        setCaptureScreen(false);
        // Let the Storyteller see the reveal as soon as the screenshot is taken.
        // JPEG encoding and database saving continue after the DOM is restored.
        resolveRevealReady();
      }
      // Start server processing with the unmodified PNG immediately after
      // capture. Local JPEG encoding is retained for download/fallback.
      if (screenshot && isDesktopApp()) {
        state.stagePromise = prepareStage(screenshot, cycle);
      }
      // Windows keeps its original PNG locally as a durable fallback.
      // JPEG conversion is handled by the relay, not the Storyteller's PC.
      const blob = screenshot || await encodeForDiscord(canvas);
      const stamp = new Date(capturedAt).toISOString().replace(/[:.]/g, "-");
      const capture = {
        id: `${capturedAt}-${Math.random().toString(36).slice(2, 9)}`,
        blob,
        capturedAt,
        winner,
        scriptName,
        filename: `botc-grimoire-${stamp}.${screenshot ? "png" : "jpg"}`,
        cycle,
      };
      await persistPending(capture);
      toast(
        "Final Grimoire captured. It will post when the reveal closes.",
        "success",
        6500,
      );
      return capture;
    })().catch((error) => {
      console.error(`${MOD_NAME}: capture failed`, error);
      updateStatusButton(true);
      toast(
        "The Grimoire could not be captured. Open GP to retry after the reveal.",
        "error",
        8000,
      );
      return null;
    }).finally(() => {
      // A capture failure must never strand the Storyteller at the End Game prompt.
      resolveRevealReady();
      state.capturing = false;
    });

    return state.capturePromise;
  }

  function captureToDataUrl(blob) {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(reader.result);
      reader.onerror = () => reject(reader.error);
      reader.readAsDataURL(blob);
    });
  }

  // Stage the native PNG while the Storyteller performs the reveal.
  // Only a successful Close confirmation can commit it to Discord.
  async function prepareStage(png, cycle) {
    if (!isDesktopApp()) return null;
    try {
      const form = new FormData();
      form.append("kind", "stage");
      form.append("file", png, "botc-grimoire.png");
      const abort = new AbortController();
      const timer = setTimeout(() => abort.abort(), 25000);
      let response;
      try {
        response = await fetch(UPLOAD_RELAY_URL, {
          method: "POST", headers: { "X-Grim-Stage": "1" },
          body: form, signal: abort.signal,
        });
      } finally { clearTimeout(timer); }
      if (!response.ok) throw new Error(`Staging returned HTTP ${response.status}`);
      const result = await response.json();
      if (!result.stage_id || !result.stage_token) throw new Error("Invalid staging response");
      if (state.revealCycle !== cycle) return null;
      state.staged = { cycle, id: result.stage_id, token: result.stage_token };
      return state.staged;
    } catch (error) {
      console.warn(`${MOD_NAME}: background staging unavailable; local upload remains available`, error);
      return null;
    }
  }

  async function commitStaged(capture) {
    const prepared = state.staged;
    const response = await fetch(UPLOAD_RELAY_URL, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        kind: "commit", stage_id: prepared.id, stage_token: prepared.token,
        content: buildMessage(capture),
      }),
    });
    if (response.status === 404) {
      // Expired or restarted relay: it definitely did not post this image.
      state.staged = null;
      return null;
    }
    if (!response.ok) {
      let detail = "";
      try { detail = (await response.json()).error || ""; } catch {}
      throw new Error(detail || `Staged Discord post returned HTTP ${response.status}`);
    }
    return await response.json();
  }

  function buildMessage(capture) {
    const unixTime = Math.floor(capture.capturedAt / 1000);
    const winner =
      capture.winner === "Pending" ? "Game complete" : `${capture.winner} wins`;
    return `**${capture.scriptName || "Unknown script"} — ${winner}**\n<t:${unixTime}:f>`;
  }

  function dismissGame2Prompt() {
    document.getElementById("gp-game2-prompt")?.remove();
    state.game2PromptCycle = null;
  }

  // Only ask permission when the authoritative Events service says "no_event".
  // An unavailable Events service is an error, not a reason to override.
  function askUnscheduledGame2Confirmation() {
    return new Promise((resolve) => {
      const overlay = document.createElement("div");
      overlay.id = "gp-game2-override";
      overlay.className = "gp-no-capture";
      overlay.setAttribute("role", "dialog");
      overlay.setAttribute("aria-modal", "true");
      overlay.setAttribute("aria-labelledby", "gp-game2-override-title");
      overlay.innerHTML = `<div id="gp-game2-override-panel">
        <h2 id="gp-game2-override-title">No event is currently scheduled for this time.</h2>
        <p>Are you sure you would like to send the next-game announcement?</p>
        <div id="gp-game2-override-actions">
          <button id="gp-game2-override-no" type="button">No, cancel</button>
          <button id="gp-game2-override-yes" type="button">Yes, announce</button>
        </div>
      </div>`;
      const finish = (confirmed) => {
        overlay.removeEventListener("keydown", onKey);
        overlay.remove();
        resolve(confirmed);
      };
      const onKey = (event) => {
        if (event.key === "Escape") { event.preventDefault(); finish(false); }
      };
      overlay.addEventListener("keydown", onKey);
      overlay.querySelector("#gp-game2-override-no").addEventListener("click", () => finish(false));
      overlay.querySelector("#gp-game2-override-yes").addEventListener("click", () => finish(true));
      document.body.appendChild(overlay);
      overlay.querySelector("#gp-game2-override-no").focus();
    });
  }

  async function sendGame2Request(proof, confirmUnscheduled = false) {
    const response = await fetch(UPLOAD_RELAY_URL, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        kind: "announce_game2", request_id: proof.request_id, token: proof.token,
        confirm_unscheduled: confirmUnscheduled,
      }),
    });
    let result = {};
    try { result = await response.json(); } catch {}
    if (!response.ok) throw new Error(result.error || `Game 2 returned HTTP ${response.status}`);
    return result;
  }

  // Announce only by presenting the private, single-use proof produced by
  // a successfully posted Grim. The relay validates this proof server-side.
  async function announceGame2() {
    if (state.game2Sending) return;
    const prompt = document.getElementById("gp-game2-prompt");
    if (!prompt) return;
    const send = prompt.querySelector("#gp-game2-send");
    state.game2Sending = true;
    send.disabled = true;
    try {
      // If Close was pressed before the staged upload finished, the ordinary
      // posting flow is still running. Wait, but never duplicate a Grim post.
      if (state.postAfterReveal) await state.postAfterReveal;
      const proof = state.game2Proof;
      if (!proof?.request_id || !proof?.token) {
        throw new Error("Grim posting has not succeeded. Retry it from GP before announcing Game 2.");
      }
      let result = await sendGame2Request(proof);
      if (result.confirmation_required === true && result.event_status === "no_event") {
        const confirmed = await askUnscheduledGame2Confirmation();
        if (!confirmed) return; // Token remains valid; X can dismiss the button.
        result = await sendGame2Request(proof, true);
      }
      if (result.ok !== true || !/^\d+$/.test(String(result.discord_message_id || ""))) {
        throw new Error("Events did not confirm a Discord announcement");
      }
      state.game2Proof = null;
      dismissGame2Prompt();
      toast("Next-game announcement posted.", "success");
    } catch (error) {
      toast(`Game 2 announcement not sent: ${error.message}`, "error", 9000);
      send.disabled = false;
    } finally { state.game2Sending = false; send.disabled = false; }
  }

  function showGame2Prompt(cycle) {
    if (state.revealCycle !== cycle) return;
    dismissGame2Prompt();
    const prompt = document.createElement("div");
    prompt.id = "gp-game2-prompt";
    prompt.className = "gp-no-capture";
    prompt.innerHTML = `<button id="gp-game2-send" type="button" title="Ask who is interested in another game">Announce Next Game</button><button id="gp-game2-dismiss" type="button" aria-label="Dismiss Game 2 announcement" title="Don't announce another game">✕</button>`;
    document.body.appendChild(prompt);
    state.game2PromptCycle = cycle;
    prompt.querySelector("#gp-game2-send").addEventListener("click", announceGame2);
    prompt.querySelector("#gp-game2-dismiss").addEventListener("click", dismissGame2Prompt);
  }

  async function directRelayRequest(payload) {
    let response;
    if (payload.kind === "test") {
      response = await fetch(UPLOAD_RELAY_URL, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ kind: "test" }),
      });
    } else {
      const form = new FormData();
      form.append("kind", "upload");
      form.append("content", payload.content || "");
      form.append("file", payload.blob, payload.filename || "botc-grimoire.jpg");
      response = await fetch(UPLOAD_RELAY_URL, { method: "POST", body: form });
    }
    if (!response.ok) {
      let detail = "";
      try { detail = (await response.json()).error || ""; } catch {}
      throw new Error(detail || `Upload relay returned HTTP ${response.status}`);
    }
    if (payload.kind === "upload") {
      try { return await response.json(); } catch { return { ok: true }; }
    }
    return { ok: true };
  }

  async function relayRequest(payload) {
    if (isExtension()) {
      const message = {
        type: "grim-poster-relay",
        kind: payload.kind,
        content: payload.content,
        filename: payload.filename,
      };
      if (payload.blob) message.dataUrl = await captureToDataUrl(payload.blob);
      const result = await chrome.runtime.sendMessage(message);
      if (!result?.ok) throw new Error(result?.error || "Discord upload failed");
      return result;
    }
    return await directRelayRequest(payload);
  }

  async function testConnection() {
    if (!(await saveSettings())) return;
    if (!state.config.enabled) return;
    state.ui.test.disabled = true;
    try {
      await relayRequest({ kind: "test" });
      toast("Discord connection test succeeded.", "success");
    } catch (error) {
      console.error(`${MOD_NAME}: connection test failed`, error);
      toast(`Discord connection test failed: ${error.message}`, "error", 8000);
    } finally {
      state.ui.test.disabled = false;
    }
  }

  async function uploadPending(force = false) {
    if (state.uploading) return false;
    if (!state.config?.enabled) return false;
    if (state.capturePromise) await state.capturePromise;
    if (!state.pending) {
      if (force) toast("There is no pending Grimoire to send.");
      return false;
    }

    const capture = state.pending;
    state.uploading = true;
    updatePendingControls();
    toast("Posting the final Grimoire to Discord…");
    try {
      // Prefer the staged, already-processed image if it is ready.
      // A missing/expired stage falls back to the previous direct uploader.
      if (isDesktopApp() && state.stagePromise && capture.cycle === state.revealCycle)
        await state.stagePromise;
      let result = null;
      if (isDesktopApp() && state.staged?.cycle === capture.cycle)
        result = await commitStaged(capture);
      if (!result) {
        // Offline/old relay fallback. Downsize very large native PNGs only now.
        const needsJpeg = capture.blob.size > DISCORD_FILE_LIMIT;
        const fallbackBlob = needsJpeg
          ? await encodeForDiscord(await screenshotBlobToCanvas(capture.blob))
          : capture.blob;
        result = await relayRequest({
        kind: "upload",
        content: buildMessage(capture),
        filename: needsJpeg ? capture.filename.replace(/\.png$/i, ".jpg") : capture.filename,
        blob: fallbackBlob,
        });
      }
      state.staged = null;
      if (state.pending?.id === capture.id) await clearPending();
      updatePendingControls();
      if (result?.game2_request_id && result?.game2_token) {
        state.game2Proof = { request_id: result.game2_request_id, token: result.game2_token };
      }
      toast("Final Grimoire posted to Discord.", "success", 6500);
      return true;
    } catch (error) {
      console.error(`${MOD_NAME}: upload failed`, error);
      updateStatusButton(true);
      updatePendingControls();
      toast(
        `Discord upload failed; the image was kept for retry. ${error.message}`,
        "error",
        9000,
      );
      return false;
    } finally {
      state.uploading = false;
      updatePendingControls();
    }
  }

  function downloadPending() {
    if (!state.config?.enabled) return;
    if (!state.pending?.blob) return;
    const filename = state.pending.filename;
    const url = URL.createObjectURL(state.pending.blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = filename;
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
    const separator = /Windows/i.test(navigator.userAgent) ? "\\" : "/";
    toast(
      `Downloaded to 'Downloads${separator}${filename}'.`,
      "success",
      6500,
    );
  }

  function revealHasStarted() {
    return Boolean(
      document.querySelector("#reveal.game-result") ||
      document.querySelector("#grimoire .circle .player.revealed"),
    );
  }

  async function updatePendingWinner(cycle) {
    if (state.capturePromise) await state.capturePromise;
    if (!state.pending || state.pending.cycle !== cycle) return;
    await persistPending({ ...state.pending, winner: state.selectedWinner || detectWinner() });
    updatePendingControls();
  }

  function discardPreparedReveal(cycle) {
    setTimeout(async () => {
      if (revealIsActive()) return;
      if (state.capturePromise) await state.capturePromise;
      if (!revealIsActive() && state.pending?.cycle === cycle) {
        await clearPending();
        state.capturePromise = null;
        toast("End game cancelled; its pending Grimoire was discarded.");
      }
    }, 50);
  }

  function beginCaptureCycle(winner) {
    state.revealCycle += 1;
    state.stagePromise = null;
    state.staged = null;
    state.game2Proof = null;
    dismissGame2Prompt();
    state.capturePromise = null;
    state.revealReadyPromise = null;
    state.selectedWinner = winner || null;
    if (state.pending) {
      updateStatusButton(true);
      toast(
        "A previous Grimoire is still waiting. Send, download, or discard it in GP before capturing another.",
        "error",
        9000,
      );
      return;
    }
    captureFinalGrimoire();
  }

  function installCloseHandler() {
    document.addEventListener(
      "click",
      (event) => {
        const prompt = findRevealPrompt();
        if (prompt) {
          const promptButton = event.target.closest(".button");
          const startsReveal =
            promptButton === prompt.buttons[0] ||
            promptButton === prompt.buttons[1];
          const cancelled =
            promptButton === prompt.buttons[2] ||
            event.target.classList.contains("modal-backdrop");
          if (startsReveal && state.config?.enabled && !state.replayingRevealClick) {
            event.stopImmediatePropagation();
            // Prefer the selected winner over reading the reveal DOM later:
            // background screenshot processing may outlast a very quick reveal.
            const choiceText = promptButton.textContent || "";
            const winner = /\bevil\b/i.test(choiceText)
              ? "Evil"
              : /\bgood\b/i.test(choiceText) ? "Good" : null;
            beginCaptureCycle(winner);
            Promise.resolve(state.revealReadyPromise || state.capturePromise).then(() => {
              state.replayingRevealClick = true;
              promptButton.click();
            });
            return;
          }
          if (state.replayingRevealClick) state.replayingRevealClick = false;
          if (cancelled) discardPreparedReveal(state.revealCycle);
        }

        const button = event.target.closest(
          "#reveal .info.overlay .button-group .button:last-child",
        );
        if (!button) return;
        const completed = revealHasStarted();
        const cycle = state.revealCycle;
        const captureToFinish = state.capturePromise;
        setTimeout(async () => {
          // Very fast reveals may close before background JPEG/IndexedDB work
          // finishes. Wait before checking the pending Grim to avoid losing it.
          if (captureToFinish) await captureToFinish;
          if (completed) {
            // Keep this flag across failed uploads and manual retry. Merely
            // capturing a Grim or pressing Send pending is not a completed reveal.
            if (state.pending?.cycle === cycle) {
              await persistPending({ ...state.pending, completedReveal: true });
            }
            if (state.pending?.cycle !== cycle) return;
            // The screenshot posts automatically on reveal Close; a separate
            // bottom-right control lets the Storyteller ask about another game.
            showGame2Prompt(cycle);
            state.postAfterReveal = uploadPending(true);
            await state.postAfterReveal;
            state.postAfterReveal = null;
          } else if (state.pending?.cycle === cycle) {
            await clearPending();
            toast("Reveal cancelled; its pending Grimoire was discarded.");
          }
        }, 0);
      },
      true,
    );
  }

  function checkRevealState() {
    if (state.suppressObserver) return;
    const promptActive = Boolean(findRevealPrompt());
    const active = revealIsActive();

    if (promptActive && !state.revealPromptActive && !active) {
      state.revealPromptActive = true;
    } else if (!promptActive && state.revealPromptActive) {
      state.revealPromptActive = false;
    }

    if (active && !state.revealActive) {
      state.revealActive = true;
      updatePendingWinner(state.revealCycle);
    } else if (!active && state.revealActive) {
      state.revealActive = false;
      state.capturePromise = null;
    }
  }

  function observeApplication() {
    state.observer = new MutationObserver(checkRevealState);
    state.observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class"],
      childList: true,
      subtree: true,
    });
    checkRevealState();
  }

  async function init() {
    injectStyles();
    createInterface();
    await loadPending();
    state.config = await getConfig();
    installCloseHandler();
    observeApplication();
    console.info(`${MOD_NAME} ${MOD_VERSION} loaded`);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init, { once: true });
  } else {
    init();
  }
})();
