// chart.js — dependency-free canvas rendering: candles + volume + grid + axes +
// crosshair + last-price marker for the main chart, equity curves for backtest
// cards, and tiny sparklines for the watchlist.

import {
  clamp, fmtDate, fmtDayMonth, fmtHM, fmtNum, fmtPrice, fmtVolume,
  niceStep, periodLabel,
} from './utils.js';

const COLORS = {
  up: '#0f9d6b',
  down: '#e5484d',
  grid: '#eef0f3',
  axisText: '#7a828c',
  text: '#1b1f24',
  crosshair: '#8b939d',
  volumeUp: 'rgba(15, 157, 107, 0.26)',
  volumeDown: 'rgba(229, 72, 77, 0.24)',
  overlay: '#d99a00',
  overlayDark: '#1b1f24',
  benchmark: '#9aa2ac',
  bg: '#ffffff',
};

function dpr() {
  return Math.max(1, Math.min(2.5, window.devicePixelRatio || 1));
}

function crisp(v) {
  return Math.round(v) + 0.5;
}

/** Backend backtest.Result.equity uses {t,value}; mock fixtures use {time,equity}. */
export function equityValue(point) {
  if (!point) return 0;
  if (Number.isFinite(point.equity)) return point.equity;
  if (Number.isFinite(point.value)) return point.value;
  return 0;
}

export function equityTime(point) {
  if (!point) return '';
  return point.time || point.t || '';
}

function priceDecimals(price) {
  if (!Number.isFinite(price)) return 2;
  if (price >= 1000) return 1;
  if (price >= 10) return 2;
  return 3;
}

export class CandleChart {
  constructor(canvas, tooltipEl = null) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.tooltip = tooltipEl;
    this.candles = [];
    this.instrument = null;
    this.periodSec = 300;
    this.overlay = null;
    this.crosshair = null;
    this.hoverIndex = -1;
    this.layout = null;
    this.type = 'candles';
    this.pad = { top: 16, right: 66, bottom: 26, left: 12 };
    this.resizeObserver = typeof ResizeObserver !== 'undefined'
      ? new ResizeObserver(() => this.resize())
      : null;
    if (this.resizeObserver && canvas.parentElement) this.resizeObserver.observe(canvas.parentElement);
    canvas.addEventListener('mousemove', (event) => this.onMove(event));
    canvas.addEventListener('mouseleave', () => this.onLeave());
    window.addEventListener('resize', () => this.resize());
  }

  setData({ candles, instrument, periodSec, overlay } = {}) {
    if (Array.isArray(candles)) this.candles = candles;
    if (instrument) this.instrument = instrument;
    if (periodSec) this.periodSec = periodSec;
    if (overlay !== undefined) this.overlay = overlay;
    this.crosshair = null;
    this.hideTooltip();
    this.draw();
  }

  setOverlay(result) {
    this.overlay = result || null;
    this.draw();
  }

  clearOverlay() {
    this.overlay = null;
    this.draw();
  }

  resize() {
    const parent = this.canvas.parentElement;
    if (!parent) return;
    const rect = parent.getBoundingClientRect();
    const width = Math.max(160, Math.floor(rect.width));
    const height = Math.max(120, Math.floor(rect.height));
    const ratio = dpr();
    this.canvas.width = Math.floor(width * ratio);
    this.canvas.height = Math.floor(height * ratio);
    this.canvas.style.width = `${width}px`;
    this.canvas.style.height = `${height}px`;
    this.ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
    this.width = width;
    this.height = height;
    this.draw();
  }

  hideTooltip() {
    if (this.tooltip) this.tooltip.hidden = true;
  }

  onLeave() {
    this.crosshair = null;
    this.hideTooltip();
    this.draw();
  }

  onMove(event) {
    if (!this.layout || !this.candles.length) return;
    const rect = this.canvas.getBoundingClientRect();
    const x = event.clientX - rect.left;
    const y = event.clientY - rect.top;
    const { plotLeft, plotWidth, count } = this.layout;
    if (x < plotLeft || x > plotLeft + plotWidth || y < this.pad.top || y > this.layout.plotBottom) {
      this.onLeave();
      return;
    }
    const index = clamp(Math.floor(((x - plotLeft) / plotWidth) * count), 0, count - 1);
    this.crosshair = { x, y, index };
    this.draw();
    this.showTooltip(index, x, y);
  }

  showTooltip(index, x, y) {
    if (!this.tooltip) return;
    const bar = this.candles[index];
    if (!bar) return;
    const instrument = this.instrument || {};
    const decimals = instrument.decimals ?? priceDecimals(bar.c);
    const prev = index > 0 ? this.candles[index - 1].c : bar.o;
    const change = bar.c - prev;
    const changePct = prev ? (change / prev) * 100 : 0;
    const cls = change >= 0 ? 'up' : 'down';
    const multiDay = (this.candles[this.candles.length - 1].t - this.candles[0].t) >= 86400;
    const timeLabel = this.periodSec >= 86400 ? fmtDate(bar.t * 1000)
      : multiDay ? `${fmtDayMonth(bar.t * 1000)} ${fmtHM(bar.t * 1000)}` : fmtHM(bar.t * 1000);
    this.tooltip.innerHTML =
      `<div class="ct-head"><span class="ct-time">${timeLabel}</span>` +
      `<span class="ct-change ${cls}">${change >= 0 ? '+' : ''}${fmtNum(change, decimals)} (${changePct >= 0 ? '+' : ''}${fmtNum(changePct, 2)}%)</span></div>` +
      `<div class="ct-rows">` +
      `<span>O</span><b>${fmtPrice(bar.o, decimals)}</b><span>H</span><b>${fmtPrice(bar.h, decimals)}</b>` +
      `<span>L</span><b>${fmtPrice(bar.l, decimals)}</b><span>C</span><b>${fmtPrice(bar.c, decimals)}</b>` +
      `<span>V</span><b>${fmtVolume(bar.v)}</b>` +
      `</div>`;
    this.tooltip.hidden = false;
    const rect = this.canvas.getBoundingClientRect();
    const width = this.tooltip.offsetWidth || 190;
    const height = this.tooltip.offsetHeight || 64;
    let left = x + 16;
    let top = y + 14;
    if (left + width > rect.width - 8) left = x - width - 16;
    if (top + height > rect.height - 8) top = y - height - 14;
    this.tooltip.style.left = `${Math.max(4, left)}px`;
    this.tooltip.style.top = `${Math.max(4, top)}px`;
  }

  // ---- drawing ---------------------------------------------------------

  draw() {
    const ctx = this.ctx;
    if (!ctx || !this.width) return;
    ctx.clearRect(0, 0, this.width, this.height);
    ctx.fillStyle = COLORS.bg;
    ctx.fillRect(0, 0, this.width, this.height);

    if (!this.candles.length) {
      this.layout = null;
      ctx.fillStyle = '#9aa2ac';
      ctx.font = '13px -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif';
      ctx.textAlign = 'center';
      ctx.fillText('Нет данных для отображения', this.width / 2, this.height / 2);
      return;
    }

    const n = this.candles.length;
    const multiDay = (this.candles[n - 1].t - this.candles[0].t) >= 86400;
    this.multiDay = multiDay;
    const twoLineAxis = multiDay && this.periodSec < 86400;
    const padBottom = twoLineAxis ? 38 : 26;
    const plotLeft = this.pad.left;
    const plotRight = this.width - this.pad.right;
    const plotTop = this.pad.top;
    const plotBottom = this.height - padBottom;
    const plotWidth = Math.max(10, plotRight - plotLeft);
    const plotHeight = Math.max(40, plotBottom - plotTop);
    const volumeHeight = Math.round(plotHeight * 0.18);
    const priceHeight = plotHeight - volumeHeight - 8;
    const priceTop = plotTop;
    const priceBottom = plotTop + priceHeight;
    const volumeTop = priceBottom + 8;
    const volumeBottom = plotBottom;

    let high = -Infinity;
    let low = Infinity;
    let maxVolume = 1;
    for (const bar of this.candles) {
      if (bar.h > high) high = bar.h;
      if (bar.l < low) low = bar.l;
      if (bar.v > maxVolume) maxVolume = bar.v;
    }
    const range = high - low || Math.abs(high) * 0.01 || 1;
    high += range * 0.06;
    low -= range * 0.06;
    const fullRange = high - low || 1;
    const stepX = plotWidth / n;
    const bodyWidth = clamp(stepX * 0.62, 1, 14);
    const decimals = this.instrument?.decimals ?? priceDecimals(this.candles[n - 1].c);

    this.layout = {
      plotLeft, plotRight, plotTop, plotBottom, plotWidth, plotHeight,
      priceTop, priceBottom, stepX, count: n, high, low, decimals,
    };

    const yPrice = (price) => priceTop + ((high - price) / fullRange) * priceHeight;

    // horizontal price grid + right axis labels
    const priceStep = niceStep(fullRange, Math.max(3, Math.round(priceHeight / 52)));
    const firstLine = Math.ceil(low / priceStep) * priceStep;
    ctx.lineWidth = 1;
    ctx.strokeStyle = COLORS.grid;
    ctx.fillStyle = COLORS.axisText;
    ctx.font = '10.5px ui-monospace, SFMono-Regular, Menlo, "Segoe UI", sans-serif';
    ctx.textAlign = 'left';
    ctx.textBaseline = 'middle';
    for (let price = firstLine; price <= high; price += priceStep) {
      const y = crisp(yPrice(price));
      ctx.beginPath();
      ctx.moveTo(plotLeft, y);
      ctx.lineTo(plotRight, y);
      ctx.stroke();
      ctx.fillText(fmtPrice(price, decimals), plotRight + 8, y);
    }

    // vertical time grid + bottom labels
    const targetTicks = Math.max(2, Math.floor(plotWidth / 130));
    const indexes = [];
    const every = Math.max(1, Math.round(n / targetTicks));
    for (let i = 0; i < n; i += every) indexes.push(i);
    if (indexes[indexes.length - 1] !== n - 1) indexes.push(n - 1);
    ctx.strokeStyle = COLORS.grid;
    ctx.fillStyle = COLORS.axisText;
    ctx.textAlign = 'center';
    ctx.textBaseline = 'top';
    let previousDay = null;
    for (const i of indexes) {
      const x = crisp(plotLeft + (i + 0.5) * stepX);
      ctx.beginPath();
      ctx.moveTo(x, plotTop);
      ctx.lineTo(x, volumeBottom);
      ctx.stroke();
      const dayKey = fmtDayMonth(this.candles[i].t * 1000);
      if (twoLineAxis) {
        ctx.fillStyle = COLORS.axisText;
        ctx.font = '10.5px ui-monospace, SFMono-Regular, Menlo, "Segoe UI", sans-serif';
        ctx.fillText(fmtHM(this.candles[i].t * 1000), x, plotBottom + 7);
        ctx.fillStyle = '#a6adb6';
        ctx.font = '9px ui-monospace, SFMono-Regular, Menlo, "Segoe UI", sans-serif';
        ctx.fillText(dayKey, x, plotBottom + 20);
      } else {
        const label = this.periodSec >= 86400 ? dayKey : fmtHM(this.candles[i].t * 1000);
        ctx.fillText(label, x, plotBottom + 8);
      }
      previousDay = dayKey;
    }

    // volume bars
    for (let i = 0; i < n; i += 1) {
      const bar = this.candles[i];
      const x = plotLeft + (i + 0.5) * stepX;
      const height = clamp((bar.v / maxVolume) * volumeHeight, 1, volumeHeight);
      ctx.fillStyle = bar.c >= bar.o ? COLORS.volumeUp : COLORS.volumeDown;
      const width = Math.max(1, bodyWidth);
      ctx.fillRect(x - width / 2, volumeBottom - height, width, height);
    }
    ctx.fillStyle = COLORS.axisText;
    ctx.font = '9.5px ui-monospace, SFMono-Regular, Menlo, "Segoe UI", sans-serif';
    ctx.textAlign = 'left';
    ctx.textBaseline = 'alphabetic';
    ctx.fillText(`V ${fmtVolume(maxVolume)}`, plotLeft + 2, volumeTop + 9);

    // price series: candles (default) or a close line
    if (this.type === 'line') {
      const firstClose = this.candles[0].c;
      const lastClose = this.candles[n - 1].c;
      const lineColor = lastClose >= firstClose ? COLORS.up : COLORS.down;
      ctx.strokeStyle = lineColor;
      ctx.lineWidth = 1.8;
      ctx.lineJoin = 'round';
      ctx.beginPath();
      for (let i = 0; i < n; i += 1) {
        const x = plotLeft + (i + 0.5) * stepX;
        const y = yPrice(this.candles[i].c);
        if (i === 0) ctx.moveTo(x, y); else ctx.lineTo(x, y);
      }
      ctx.stroke();
      const gradient = ctx.createLinearGradient(0, priceTop, 0, priceBottom);
      gradient.addColorStop(0, lastClose >= firstClose ? 'rgba(15, 157, 107, 0.16)' : 'rgba(229, 72, 77, 0.16)');
      gradient.addColorStop(1, 'rgba(255, 255, 255, 0)');
      ctx.lineTo(plotLeft + (n - 0.5) * stepX, priceBottom);
      ctx.lineTo(plotLeft + 0.5 * stepX, priceBottom);
      ctx.closePath();
      ctx.fillStyle = gradient;
      ctx.fill();
    } else {
    // candles
    for (let i = 0; i < n; i += 1) {
      const bar = this.candles[i];
      const up = bar.c >= bar.o;
      const color = up ? COLORS.up : COLORS.down;
      const x = plotLeft + (i + 0.5) * stepX;
      const yOpen = yPrice(bar.o);
      const yClose = yPrice(bar.c);
      const yHigh = yPrice(bar.h);
      const yLow = yPrice(bar.l);
      ctx.strokeStyle = color;
      ctx.fillStyle = color;
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(crisp(x), yHigh);
      ctx.lineTo(crisp(x), yLow);
      ctx.stroke();
      const bodyTop = Math.min(yOpen, yClose);
      const bodyHeight = Math.max(1, Math.abs(yClose - yOpen));
      const width = Math.max(1, bodyWidth);
      ctx.fillRect(x - width / 2, bodyTop, width, bodyHeight);
    }
    }

    // last price marker + dashed line
    const lastBar = this.candles[n - 1];
    const lastColor = lastBar.c >= lastBar.o ? COLORS.up : COLORS.down;
    const lastY = yPrice(lastBar.c);
    ctx.save();
    ctx.strokeStyle = lastColor;
    ctx.globalAlpha = 0.5;
    ctx.setLineDash([3, 3]);
    ctx.beginPath();
    ctx.moveTo(plotLeft, crisp(lastY));
    ctx.lineTo(plotRight, crisp(lastY));
    ctx.stroke();
    ctx.restore();
    const label = fmtPrice(lastBar.c, decimals);
    ctx.font = '10.5px ui-monospace, SFMono-Regular, Menlo, "Segoe UI", sans-serif';
    const labelWidth = ctx.measureText(label).width + 12;
    const labelHeight = 18;
    const labelX = plotRight + 4;
    const labelY = clamp(lastY - labelHeight / 2, priceTop, priceBottom - labelHeight);
    roundRect(ctx, labelX, labelY, Math.max(30, labelWidth), labelHeight, 4);
    ctx.fillStyle = lastColor;
    ctx.fill();
    ctx.fillStyle = '#ffffff';
    ctx.textAlign = 'left';
    ctx.textBaseline = 'middle';
    ctx.fillText(label, labelX + 6, labelY + labelHeight / 2 + 0.5);

    // overlay: equity line + benchmark + trade markers (own vertical scale)
    if (this.overlay) {
      this.drawOverlay(ctx, {
        n, stepX, plotLeft, priceTop, priceBottom, high, low, yPrice,
      });
    }

    // crosshair
    if (this.crosshair) {
      const { index, y } = this.crosshair;
      const bar = this.candles[index];
      if (bar) {
        const x = crisp(plotLeft + (index + 0.5) * stepX);
        ctx.save();
        ctx.strokeStyle = COLORS.crosshair;
        ctx.setLineDash([3, 3]);
        ctx.lineWidth = 1;
        ctx.beginPath();
        ctx.moveTo(x, plotTop);
        ctx.lineTo(x, volumeBottom);
        ctx.stroke();
        const yLine = clamp(y, priceTop, priceBottom);
        ctx.beginPath();
        ctx.moveTo(plotLeft, crisp(yLine));
        ctx.lineTo(plotRight, crisp(yLine));
        ctx.stroke();
        ctx.restore();
        // hovered close dot
        ctx.fillStyle = lastColor;
        ctx.beginPath();
        ctx.arc(x, yPrice(bar.c), 2.4, 0, Math.PI * 2);
        ctx.fill();
        // hovered time marker
        const crossMultiDay = (this.candles[n - 1].t - this.candles[0].t) >= 86400;
        const timeLabel = this.periodSec >= 86400 ? fmtDayMonth(bar.t * 1000)
          : crossMultiDay ? `${fmtDayMonth(bar.t * 1000)} ${fmtHM(bar.t * 1000)}` : fmtHM(bar.t * 1000);
        ctx.font = '10px ui-monospace, SFMono-Regular, Menlo, "Segoe UI", sans-serif';
        const width = ctx.measureText(timeLabel).width + 12;
        const boxX = clamp(x - width / 2, plotLeft, plotRight - width);
        roundRect(ctx, boxX, plotBottom + 4, width, 17, 3);
        ctx.fillStyle = '#23262b';
        ctx.fill();
        ctx.fillStyle = '#ffffff';
        ctx.textAlign = 'center';
        ctx.textBaseline = 'middle';
        ctx.fillText(timeLabel, boxX + width / 2, plotBottom + 13);
        // hovered price marker on the right axis
        const priceAtY = high - ((clamp(y, priceTop, priceBottom) - priceTop) / (priceBottom - priceTop)) * (high - low);
        const marker = fmtPrice(priceAtY, decimals);
        ctx.fillStyle = '#23262b';
        const mw = ctx.measureText(marker).width + 12;
        roundRect(ctx, plotRight + 4, clamp(y - 8, priceTop, priceBottom - 16), Math.max(30, mw), 16, 3);
        ctx.fill();
        ctx.fillStyle = '#ffffff';
        ctx.textAlign = 'left';
        ctx.fillText(marker, plotRight + 10, clamp(y - 8, priceTop, priceBottom - 16) + 8);
      }
    }
  }

  drawOverlay(ctx, { n, stepX, plotLeft, priceTop, priceBottom, high, low, yPrice }) {
    const points = this.overlay.equity || [];
    if (points.length < 2) return;
    const startTime = this.candles[0].t;
    const endTime = this.candles[n - 1].t;
    const span = endTime - startTime || 1;
    const mapIndex = (timeIso) => {
      const seconds = (Date.parse(timeIso) || 0) / 1000;
      return clamp((seconds - startTime) / span, 0, 1) * (n - 1);
    };
    // Equity lives on its own scale and is normalised into the price plot area
    // (its absolute values are millions, while prices are hundreds).
    const eqValues = [];
    for (const point of points) {
      eqValues.push(equityValue(point));
      if (Number.isFinite(point.benchmark)) eqValues.push(point.benchmark);
    }
    const eqMin = Math.min(...eqValues);
    const eqMax = Math.max(...eqValues);
    const eqRange = eqMax - eqMin || Math.abs(eqMax) * 0.01 || 1;
    const pad = (priceBottom - priceTop) * 0.06;
    const top = priceTop + pad;
    const bottom = priceBottom - pad;
    const yOf = (value) => bottom - ((value - eqMin) / eqRange) * (bottom - top);
    const drawLine = (values, color, dash, width) => {
      ctx.save();
      ctx.strokeStyle = color;
      ctx.lineWidth = width;
      ctx.setLineDash(dash);
      ctx.beginPath();
      values.forEach((value, i) => {
        const x = plotLeft + (mapIndex(equityTime(points[i])) + 0.5) * stepX;
        const y = yOf(value);
        if (i === 0) ctx.moveTo(x, y); else ctx.lineTo(x, y);
      });
      ctx.stroke();
      ctx.restore();
    };
    drawLine(points.map((point) => point.benchmark), COLORS.benchmark, [4, 4], 1);
    drawLine(points.map((point) => equityValue(point)), COLORS.overlay, [], 1.8);

    // trade markers: entry ▲ below the bar, exit ▼ above the bar
    const trades = (this.overlay.trades || []).slice(0, 120);
    const lastPriceBar = this.candles[n - 1];
    const priceSpan = high - low || 1;
    for (const trade of trades) {
      for (const [timeKey, side] of [['entryTime', 'entry'], ['exitTime', 'exit']]) {
        if (!trade[timeKey]) continue;
        const index = Math.round(mapIndex(trade[timeKey]));
        const bar = this.candles[clamp(index, 0, n - 1)] || lastPriceBar;
        const x = plotLeft + (clamp(index, 0, n - 1) + 0.5) * stepX;
        const isEntry = side === 'entry';
        const baseY = isEntry ? yPrice(bar.l) : yPrice(bar.h);
        const dir = isEntry ? 1 : -1;
        const y = baseY + dir * 7;
        const color = trade.pnl >= 0 ? COLORS.up : COLORS.down;
        ctx.fillStyle = color;
        ctx.beginPath();
        if (isEntry) {
          ctx.moveTo(x, y);
          ctx.lineTo(x - 4, y + 6);
          ctx.lineTo(x + 4, y + 6);
        } else {
          ctx.moveTo(x, y);
          ctx.lineTo(x - 4, y - 6);
          ctx.lineTo(x + 4, y - 6);
        }
        ctx.closePath();
        ctx.fill();
        void priceSpan;
      }
    }

    // legend
    const legend = [
      { label: 'equity', color: COLORS.overlay, dash: [] },
      { label: 'бенчмарк', color: COLORS.benchmark, dash: [4, 4] },
    ];
    ctx.save();
    ctx.font = '10px -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif';
    ctx.textAlign = 'left';
    ctx.textBaseline = 'top';
    let x = plotLeft + 8;
    const y = priceTop + 4;
    for (const item of legend) {
      ctx.strokeStyle = item.color;
      ctx.lineWidth = 2;
      ctx.setLineDash(item.dash);
      ctx.beginPath();
      ctx.moveTo(x, y + 5);
      ctx.lineTo(x + 14, y + 5);
      ctx.stroke();
      ctx.setLineDash([]);
      ctx.fillStyle = COLORS.axisText;
      ctx.fillText(item.label, x + 18, y);
      x += 18 + ctx.measureText(item.label).width + 14;
    }
    ctx.restore();
  }
}

function roundRect(ctx, x, y, width, height, radius) {
  const r = Math.min(radius, height / 2, width / 2);
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.lineTo(x + width - r, y);
  ctx.quadraticCurveTo(x + width, y, x + width, y + r);
  ctx.lineTo(x + width, y + height - r);
  ctx.quadraticCurveTo(x + width, y + height, x + width - r, y + height);
  ctx.lineTo(x + r, y + height);
  ctx.quadraticCurveTo(x, y + height, x, y + height - r);
  ctx.lineTo(x, y + r);
  ctx.quadraticCurveTo(x, y, x + r, y);
  ctx.closePath();
}

/** Draw a compact price sparkline into a small canvas (watchlist rows). */
export function drawSparkline(canvas, values, { positive = null } = {}) {
  if (!canvas) return;
  const parent = canvas.parentElement;
  if (!parent) return;
  const width = Math.max(40, canvas.clientWidth || parent.clientWidth || 76);
  const height = Math.max(18, canvas.clientHeight || parent.clientHeight || 26);
  const ratio = dpr();
  canvas.width = Math.floor(width * ratio);
  canvas.height = Math.floor(height * ratio);
  const ctx = canvas.getContext('2d');
  ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
  ctx.clearRect(0, 0, width, height);
  const list = (values || []).filter((v) => Number.isFinite(Number(v))).map(Number);
  if (list.length < 2) {
    ctx.strokeStyle = '#d8dce1';
    ctx.beginPath();
    ctx.moveTo(2, height / 2);
    ctx.lineTo(width - 2, height / 2);
    ctx.stroke();
    return;
  }
  const isUp = positive === null ? list[list.length - 1] >= list[0] : positive;
  const color = isUp ? COLORS.up : COLORS.down;
  const min = Math.min(...list);
  const max = Math.max(...list);
  const range = max - min || Math.abs(max) * 0.01 || 1;
  const stepX = (width - 4) / (list.length - 1);
  const yOf = (value) => 3 + ((max - value) / range) * (height - 6);

  // light area under the line
  const gradient = ctx.createLinearGradient(0, 0, 0, height);
  gradient.addColorStop(0, isUp ? 'rgba(15, 157, 107, 0.16)' : 'rgba(229, 72, 77, 0.16)');
  gradient.addColorStop(1, 'rgba(255, 255, 255, 0)');
  ctx.beginPath();
  list.forEach((value, index) => {
    const x = 2 + index * stepX;
    const y = yOf(value);
    if (index === 0) ctx.moveTo(x, y); else ctx.lineTo(x, y);
  });
  ctx.lineTo(width - 2, height);
  ctx.lineTo(2, height);
  ctx.closePath();
  ctx.fillStyle = gradient;
  ctx.fill();

  ctx.strokeStyle = color;
  ctx.lineWidth = 1.4;
  ctx.lineJoin = 'round';
  ctx.beginPath();
  list.forEach((value, index) => {
    const x = 2 + index * stepX;
    const y = yOf(value);
    if (index === 0) ctx.moveTo(x, y); else ctx.lineTo(x, y);
  });
  ctx.stroke();

  // last point dot
  const lastX = 2 + (list.length - 1) * stepX;
  const lastY = yOf(list[list.length - 1]);
  if (stepX > 1.2) {
    ctx.fillStyle = color;
    ctx.beginPath();
    ctx.arc(lastX, lastY, 1.9, 0, Math.PI * 2);
    ctx.fill();
  }
}

/** Equity curve for a backtest card. */
export class EquityChart {
  constructor(canvas) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.points = [];
    this.metrics = null;
    this.resizeObserver = typeof ResizeObserver !== 'undefined'
      ? new ResizeObserver(() => this.resize())
      : null;
    if (this.resizeObserver && canvas.parentElement) this.resizeObserver.observe(canvas.parentElement);
    window.addEventListener('resize', () => this.resize());
  }

  setData(result) {
    this.points = (result && result.equity) || [];
    this.metrics = (result && result.metrics) || null;
    this.resize();
  }

  resize() {
    const parent = this.canvas.parentElement;
    if (!parent) return;
    const rect = parent.getBoundingClientRect();
    const width = Math.max(120, Math.floor(rect.width));
    const height = Math.max(60, Math.floor(rect.height));
    const ratio = dpr();
    this.canvas.width = Math.floor(width * ratio);
    this.canvas.height = Math.floor(height * ratio);
    this.canvas.style.width = `${width}px`;
    this.canvas.style.height = `${height}px`;
    this.ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
    this.width = width;
    this.height = height;
    this.draw();
  }

  draw() {
    const ctx = this.ctx;
    if (!ctx || !this.width) return;
    ctx.clearRect(0, 0, this.width, this.height);
    if (this.points.length < 2) {
      ctx.fillStyle = '#9aa2ac';
      ctx.font = '11px -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif';
      ctx.textAlign = 'center';
      ctx.fillText('нет данных equity', this.width / 2, this.height / 2);
      return;
    }
    const pad = { top: 8, right: 48, bottom: 6, left: 4 };
    const width = this.width - pad.left - pad.right;
    const height = this.height - pad.top - pad.bottom;
    const values = this.points.flatMap((point) => [equityValue(point), point.benchmark]).filter((v) => Number.isFinite(v));
    const min = Math.min(...values);
    const max = Math.max(...values);
    const range = max - min || Math.abs(max) * 0.01 || 1;
    const stepX = width / (this.points.length - 1);
    const yOf = (value) => pad.top + ((max - value) / range) * height;
    const xOf = (index) => pad.left + index * stepX;
    const positive = equityValue(this.points[this.points.length - 1]) >= equityValue(this.points[0]);
    const color = positive ? COLORS.up : COLORS.down;

    // baseline
    ctx.strokeStyle = '#eef0f3';
    ctx.lineWidth = 1;
    [0, 0.5, 1].forEach((fraction) => {
      const y = crisp(pad.top + fraction * height);
      ctx.beginPath();
      ctx.moveTo(pad.left, y);
      ctx.lineTo(pad.left + width, y);
      ctx.stroke();
    });

    // benchmark
    ctx.strokeStyle = COLORS.benchmark;
    ctx.lineWidth = 1;
    ctx.setLineDash([3, 3]);
    ctx.beginPath();
    this.points.forEach((point, index) => {
      const y = yOf(point.benchmark);
      if (index === 0) ctx.moveTo(xOf(index), y); else ctx.lineTo(xOf(index), y);
    });
    ctx.stroke();
    ctx.setLineDash([]);

    // equity area + line
    const gradient = ctx.createLinearGradient(0, pad.top, 0, pad.top + height);
    gradient.addColorStop(0, positive ? 'rgba(15, 157, 107, 0.20)' : 'rgba(229, 72, 77, 0.20)');
    gradient.addColorStop(1, 'rgba(255, 255, 255, 0)');
    ctx.beginPath();
    this.points.forEach((point, index) => {
      const y = yOf(equityValue(point));
      if (index === 0) ctx.moveTo(xOf(index), y); else ctx.lineTo(xOf(index), y);
    });
    ctx.lineTo(xOf(this.points.length - 1), pad.top + height);
    ctx.lineTo(xOf(0), pad.top + height);
    ctx.closePath();
    ctx.fillStyle = gradient;
    ctx.fill();

    ctx.strokeStyle = color;
    ctx.lineWidth = 1.7;
    ctx.lineJoin = 'round';
    ctx.beginPath();
    this.points.forEach((point, index) => {
      const y = yOf(equityValue(point));
      if (index === 0) ctx.moveTo(xOf(index), y); else ctx.lineTo(xOf(index), y);
    });
    ctx.stroke();

    // start/end labels on the right axis
    ctx.fillStyle = COLORS.axisText;
    ctx.font = '9.5px ui-monospace, SFMono-Regular, Menlo, "Segoe UI", sans-serif';
    ctx.textAlign = 'left';
    ctx.textBaseline = 'middle';
    const fmtEquity = (value) => `${(value / 1e6).toFixed(2)} млн`;
    ctx.fillText(fmtEquity(max), this.width - pad.right + 5, pad.top + 4);
    ctx.fillText(fmtEquity(min), this.width - pad.right + 5, pad.top + height - 4);
    void periodLabel;
  }
}
