(() => {
  "use strict";

  const csrf = document.querySelector('meta[name="csrf-token"]').content;
  const cards = new Map([...document.querySelectorAll(".service-card")].map(card => [card.dataset.service, card]));
  const serviceState = new Map();
  const dialog = document.getElementById("confirm-dialog");
  const toast = document.getElementById("toast");
  let pendingStop = null;
  let polling = false;
  let toastTimer = 0;

  const formatDuration = seconds => {
    const safe = Math.max(0, Math.floor(seconds || 0));
    const hours = String(Math.floor(safe / 3600)).padStart(2, "0");
    const minutes = String(Math.floor((safe % 3600) / 60)).padStart(2, "0");
    const secs = String(safe % 60).padStart(2, "0");
    return `${hours}:${minutes}:${secs}`;
  };

  const remainingFor = service => {
    if (!service?.deadline_unix) return 0;
    return Math.max(0, service.deadline_unix - Date.now() / 1000);
  };

  const showToast = (message, isError = false) => {
    window.clearTimeout(toastTimer);
    toast.textContent = message;
    toast.classList.toggle("error", isError);
    toast.classList.add("show");
    toastTimer = window.setTimeout(() => toast.classList.remove("show"), 4200);
  };

  const request = async (url, options = {}) => {
    const response = await fetch(url, {
      ...options,
      headers: { "X-CSRF-Token": csrf, ...(options.headers || {}) },
    });
    if (response.status === 401) {
      window.location.assign("/login");
      throw new Error("登录状态已失效");
    }
    const data = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(data.error || "请求失败，请稍后重试");
    return data;
  };

  const updateCard = service => {
    const card = cards.get(service.id);
    if (!card) return;
    serviceState.set(service.id, service);
    const toggle = card.querySelector(".power-switch");
    const stop = card.querySelector(".stop-button");
    const extend = card.querySelector(".extend-button");
    const status = card.querySelector(".service-status");
    const detail = card.querySelector(".service-detail");

    card.classList.toggle("is-running", service.running);
    card.classList.toggle("has-error", Boolean(service.error));
    toggle.setAttribute("aria-checked", String(service.on));
    toggle.setAttribute("aria-label", `${service.on ? "关闭" : "启动"}${service.name}`);
    toggle.querySelector(".switch-label").textContent = service.on ? "开启" : "关闭";
    toggle.disabled = Boolean(service.busy) || !service.available;
    stop.disabled = !service.on || Boolean(service.busy);
    status.textContent = service.status;
    detail.textContent = service.error || service.detail;
    card.querySelector(".service-notice").textContent = service.notice || "";
    card.querySelector(".extension-count").textContent = service.extensions ? `已续时 ${service.extensions} 次` : "";
    renderTimer(service.id);
  };

  const renderTimer = id => {
    const service = serviceState.get(id);
    const card = cards.get(id);
    if (!service || !card) return;
    const remaining = remainingFor(service);
    const countdown = card.querySelector(".countdown");
    const deadline = card.querySelector(".deadline-label");
    const rail = card.querySelector(".timer-rail i");
    const extend = card.querySelector(".extend-button");

    countdown.textContent = service.running && service.deadline_unix ? formatDuration(remaining) : "--:--:--";
    if (service.running && service.deadline_unix) {
      const date = new Date(service.deadline_unix * 1000);
      deadline.textContent = `计划关闭时间 · ${date.toLocaleString("zh-CN", { hour12: false })}`;
      const cycle = Math.max(3600, 14400 + service.extensions * 3600);
      rail.style.width = `${Math.min(100, Math.max(0, remaining / cycle * 100))}%`;
    } else {
      deadline.textContent = "启动后将在此显示关闭时间";
      rail.style.width = "0%";
    }
    const canExtendNow = service.running && !service.busy && remaining > 0 && remaining <= 3600;
    extend.disabled = !canExtendNow;
  };

  const applyServices = services => services.forEach(updateCard);

  const poll = async () => {
    if (polling) return;
    polling = true;
    try {
      const data = await request("/api/status");
      applyServices(data.services);
      document.getElementById("sync-status").textContent = `状态已同步 · ${new Date().toLocaleTimeString("zh-CN", { hour12: false })}`;
    } catch (error) {
      document.getElementById("sync-status").textContent = "状态同步失败，正在重试";
      if (error.message !== "登录状态已失效") showToast(error.message, true);
    } finally {
      polling = false;
    }
  };

  const perform = async (id, action) => {
    const card = cards.get(id);
    const service = serviceState.get(id);
    if (!card || !service) return;
    const actionLabel = { start: "正在启动", stop: "正在停止", extend: "正在续时" }[action];
    service.busy = actionLabel;
    updateCard(service);
    try {
      const data = await request(`/api/services/${id}/${action}`, { method: "POST" });
      if (data.services) applyServices(data.services);
      const success = { start: `${service.name}已启动`, stop: `${service.name}已关闭`, extend: `${service.name}已延长 1 小时` }[action];
      showToast(success);
    } catch (error) {
      showToast(error.message, true);
    } finally {
      await poll();
    }
  };

  const askToStop = id => {
    const service = serviceState.get(id);
    if (!service) return;
    pendingStop = id;
    document.getElementById("confirm-service-name").textContent = service.name;
    document.getElementById("confirm-remaining").textContent = formatDuration(remainingFor(service));
    dialog.showModal();
  };

  cards.forEach((card, id) => {
    card.querySelector(".power-switch").addEventListener("click", () => {
      const service = serviceState.get(id);
      if (!service || service.busy) return;
      if (service.on) askToStop(id);
      else perform(id, "start");
    });
    card.querySelector(".stop-button").addEventListener("click", () => askToStop(id));
    card.querySelector(".extend-button").addEventListener("click", () => perform(id, "extend"));
  });

  dialog.addEventListener("close", () => {
    if (dialog.returnValue === "confirm" && pendingStop) perform(pendingStop, "stop");
    pendingStop = null;
  });

  document.getElementById("logout-button").addEventListener("click", async () => {
    try {
      await request("/logout", { method: "POST" });
    } finally {
      window.location.assign("/login");
    }
  });

  const tick = () => {
    document.getElementById("local-time").textContent = new Date().toLocaleTimeString("zh-CN", { hour12: false });
    serviceState.forEach((_, id) => renderTimer(id));
  };

  tick();
  poll();
  window.setInterval(tick, 500);
  window.setInterval(poll, 2000);
})();
