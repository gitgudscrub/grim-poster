(() => {
  "use strict";
  if (globalThis.__BOTC_GRIM_POSTER__) globalThis.__BOTC_GRIM_POSTER__.version = "0.2.0";
  const KEY = "botc-grim-poster-launcher-v1";
  const clamp = (value, max) => Math.max(0, Math.min(max, value));

  function install() {
    const button = document.querySelector("#gp-launch");
    const panel = document.querySelector("#gp-panel");
    const actions = panel?.querySelector(".gp-actions");
    if (!button || !panel || !actions || button.dataset.gpMovable) return;
    button.dataset.gpMovable = "1";

    let saved = {};
    try {
      saved = JSON.parse(localStorage.getItem(KEY) || "{}");
    } catch {
      saved = {};
    }
    const style = document.createElement("style");
    style.textContent = "#gp-launch{cursor:grab;touch-action:none}#gp-launch[hidden]{display:none!important}#gp-launch.gp-locked{cursor:pointer;touch-action:auto}#gp-launch.gp-dragging{cursor:grabbing}";
    document.head.appendChild(style);

    function place() {
      const rect = button.getBoundingClientRect();
      if (Number.isFinite(saved.x) && Number.isFinite(saved.y)) {
        button.style.left = `${Math.round(saved.x * Math.max(0, innerWidth - rect.width))}px`;
        button.style.top = `${Math.round(saved.y * Math.max(0, innerHeight - rect.height))}px`;
        button.style.right = "auto";
        button.style.bottom = "auto";
      }
      button.classList.toggle("gp-locked", saved.locked === true);
      button.hidden = saved.hidden === true;
    }
    place();
    window.addEventListener("resize", place);

    const label = document.createElement("label");
    label.className = "gp-check";
    const checkbox = document.createElement("input");
    checkbox.type = "checkbox";
    checkbox.checked = saved.hidden !== true;
    const text = document.createElement("span");
    text.textContent = "Show the floating GP button";
    label.append(checkbox, text);
    checkbox.addEventListener("change", () => {
      saved.hidden = !checkbox.checked;
      button.hidden = saved.hidden;
      localStorage.setItem(KEY, JSON.stringify(saved));
    });
    const lockLabel = document.createElement("label");
    lockLabel.className = "gp-check";
    const lockCheckbox = document.createElement("input");
    lockCheckbox.type = "checkbox";
    lockCheckbox.checked = saved.locked === true;
    const lockText = document.createElement("span");
    lockText.textContent = "Lock GP button in place";
    lockLabel.append(lockCheckbox, lockText);
    lockCheckbox.addEventListener("change", () => {
      saved.locked = lockCheckbox.checked;
      button.classList.toggle("gp-locked", saved.locked);
      localStorage.setItem(KEY, JSON.stringify(saved));
    });
    const note = document.createElement("p");
    note.textContent = "Unlock to move GP. Press Ctrl+Shift+G to reopen settings when it is hidden.";
    const pending = panel.querySelector("#gp-pending-message");
    panel.insertBefore(label, pending || actions);
    panel.insertBefore(lockLabel, pending || actions);
    panel.insertBefore(note, pending || actions);

    let drag = null;
    let dragged = false;
    button.addEventListener("pointerdown", (event) => {
      if (event.button !== 0 || drag || saved.locked) return;
      const rect = button.getBoundingClientRect();
      drag = { id: event.pointerId, x: event.clientX, y: event.clientY, left: rect.left, top: rect.top, moved: false };
      try { button.setPointerCapture(event.pointerId); } catch {}
    });
    button.addEventListener("pointermove", (event) => {
      if (!drag || drag.id !== event.pointerId) return;
      const dx = event.clientX - drag.x;
      const dy = event.clientY - drag.y;
      if (!drag.moved && Math.hypot(dx, dy) < 4) return;
      drag.moved = true;
      button.classList.add("gp-dragging");
      event.preventDefault();
      const rect = button.getBoundingClientRect();
      button.style.left = `${Math.round(clamp(drag.left + dx, innerWidth - rect.width))}px`;
      button.style.top = `${Math.round(clamp(drag.top + dy, innerHeight - rect.height))}px`;
      button.style.right = "auto";
      button.style.bottom = "auto";
    });
    button.addEventListener("pointerup", (event) => {
      if (!drag || drag.id !== event.pointerId) return;
      const moved = drag.moved;
      drag = null;
      button.classList.remove("gp-dragging");
      if (!moved) return;
      const rect = button.getBoundingClientRect();
      saved.x = rect.left / Math.max(1, innerWidth - rect.width);
      saved.y = rect.top / Math.max(1, innerHeight - rect.height);
      localStorage.setItem(KEY, JSON.stringify(saved));
      dragged = true;
    });
    button.addEventListener("pointercancel", () => { drag = null; button.classList.remove("gp-dragging"); });
    button.addEventListener("click", (event) => {
      if (!dragged) return;
      event.preventDefault();
      event.stopImmediatePropagation();
      dragged = false;
    }, true);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", install, { once: true });
  } else {
    install();
  }
})();
