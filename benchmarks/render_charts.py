"""Generate Light and Dark SVG charts for README.md from actual benchmark & release metrics.

Zero external dependencies (Python stdlib only). Writes:
  - assets/version-evolution-light.svg
  - assets/version-evolution-dark.svg
  - assets/benchmark-light.svg
  - assets/benchmark-dark.svg
  - assets/pipeline-light.svg
  - assets/pipeline-dark.svg
"""
from __future__ import annotations

import html
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
ASSETS = ROOT / "assets"
RESULTS_PATH = ROOT / "benchmarks/results.json"

THEMES = {
    "light": {
        "bg": "#FAF7F0",
        "card": "#FFFDF9",
        "border": "#E5DEC9",
        "grid": "#EFEADD",
        "ink": "#2E3538",
        "muted": "#6E7470",
        "accent": "#C46542",
        "accent_soft": "#F6E8E0",
        "blue": "#4E7B99",
        "blue_soft": "#E9F0F5",
        "green": "#4F7D63",
        "green_soft": "#E7F0EA",
        "bar_other": "#DDD6C7",
        "bar_other_hi": "#B8B09E",
        "pill_bg": "#F2EDE1",
        "ref_line": "#8A8F8B",
    },
    "dark": {
        "bg": "#14171A",
        "card": "#1B1F23",
        "border": "#2F353B",
        "grid": "#24292E",
        "ink": "#E7E3DA",
        "muted": "#969B97",
        "accent": "#DF7E58",
        "accent_soft": "#332119",
        "blue": "#74A3C2",
        "blue_soft": "#1B2A35",
        "green": "#77B390",
        "green_soft": "#192B22",
        "bar_other": "#30363C",
        "bar_other_hi": "#485058",
        "pill_bg": "#24292E",
        "ref_line": "#8E9490",
    },
}

FONT = "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif"
MONO = "ui-monospace, SFMono-Regular, 'SF Mono', Menlo, Consolas, monospace"


def esc(s: str) -> str:
    return html.escape(s, quote=True)


def fmt_int(n: int) -> str:
    return f"{n:,}".replace(",", " ")


def render_version_evolution(theme_name: str, t: dict[str, str]) -> str:
    # Verified historical release metrics from git tags v0.2.0..v0.4.0
    # Base RSS and load_ms from isolated subprocess benchmark (v0.2.2..v0.4.0) + v0.2.0 initial
    W, H = 960, 470
    out: list[str] = [
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}" height="{H}" role="img" aria-label="Динамика версий rulemma от v0.2.0 до v0.4.0">',
        "<title>Динамика версий rulemma v0.2.0 — v0.4.0: память, холодный старт, охват словарей и WASM</title>",
        f'<rect width="{W}" height="{H}" rx="14" fill="{t["bg"]}" stroke="{t["border"]}" stroke-width="1"/>',
        # Header (pill strictly aligned with title line y=15..41; subtitle sits cleanly below at y=60)
        f'<text x="28" y="34" font-family="{FONT}" font-size="17.5" font-weight="700" fill="{t["ink"]}">Динамика версий rulemma (v0.2.0 → v0.4.0)</text>',
        f'<text x="28" y="60" font-family="{FONT}" font-size="11.5" fill="{t["muted"]}">Словари выросли вдвое (250k → 481k), байтовая арена снизила RAM на 40.5% и ускорила старт в 2–3.2×</text>',
        # Badge top-right
        f'<rect data-pill="1" x="700" y="15" width="236" height="26" rx="13" fill="{t["accent_soft"]}" stroke="{t["accent"]}" stroke-opacity="0.35"/>',
        f'<text x="818" y="32" text-anchor="middle" font-family="{FONT}" font-size="11.5" font-weight="700" fill="{t["accent"]}">v0.4.0: 37.1 МБ RAM &lt; pymorphy3</text>',
    ]

    # --- LEFT CARD: Memory (RSS MB) & Cold Start (ms) ---
    lx, ly, lw, lh = 24, 78, 452, 266
    out.append(f'<rect x="{lx}" y="{ly}" width="{lw}" height="{lh}" rx="12" fill="{t["card"]}" stroke="{t["border"]}"/>')
    out.append(f'<text x="{lx+18}" y="{ly+26}" font-family="{FONT}" font-size="13.5" font-weight="700" fill="{t["ink"]}">Память (RSS) и холодный старт</text>')
    # Legend inside left card
    out.append(f'<circle cx="{lx+24}" cy="{ly+46}" r="4.5" fill="{t["accent"]}"/>')
    out.append(f'<text x="{lx+34}" y="{ly+50}" font-family="{FONT}" font-size="11" fill="{t["muted"]}">База RSS (МБ)</text>')
    out.append(f'<circle cx="{lx+134}" cy="{ly+46}" r="4.5" fill="{t["blue"]}"/>')
    out.append(f'<text x="{lx+144}" y="{ly+50}" font-family="{FONT}" font-size="11" fill="{t["muted"]}">Пик 20k RSS (МБ)</text>')
    out.append(f'<rect x="{lx+260}" y="{ly+42}" width="12" height="8" rx="2" fill="{t["green"]}" fill-opacity="0.32" stroke="{t["green"]}" stroke-width="1.2"/>')
    out.append(f'<text x="{lx+278}" y="{ly+50}" font-family="{FONT}" font-size="11" fill="{t["muted"]}">Старт (мс)</text>')

    # Plot area inside left card
    px, py, pw, ph = lx + 46, ly + 72, lw - 86, 152
    # Y-axis left: 0..120 MB, Y-axis right: 0..240 ms (exact 2:1 ratio!)
    for mb in (0, 30, 60, 90, 120):
        yy = py + ph - (mb / 120.0) * ph
        out.append(f'<line x1="{px}" y1="{yy:.1f}" x2="{px+pw}" y2="{yy:.1f}" stroke="{t["grid"]}" stroke-width="1"/>')
        out.append(f'<text x="{px-8}" y="{yy+3.5:.1f}" text-anchor="end" font-family="{MONO}" font-size="10" fill="{t["muted"]}">{mb}</text>')
        out.append(f'<text x="{px+pw+8}" y="{yy+3.5:.1f}" text-anchor="start" font-family="{MONO}" font-size="10" fill="{t["green"]}">{mb*2}</text>')
    out.append(f'<text x="{px-8}" y="{py-8}" text-anchor="end" font-family="{FONT}" font-size="9.5" fill="{t["muted"]}">МБ</text>')
    out.append(f'<text x="{px+pw+8}" y="{py-8}" text-anchor="start" font-family="{FONT}" font-size="9.5" fill="{t["green"]}">мс</text>')

    rel_left = [
        ("v0.2.2", 60.9, 104.1, 172),
        ("v0.3.0", 62.1, 106.1, 198),
        ("v0.3.1", 62.4, 105.2, 185),
        ("v0.4.0", 37.1, 65.3, 93),
    ]
    xs_left = [px + 32 + i * ((pw - 64) / (len(rel_left) - 1)) for i in range(len(rel_left))]

    # Reference line: pymorphy3 base RSS = 45.0 MB (label centered in clean gap between v0.2.2 and v0.3.0)
    y_pm = py + ph - (45.0 / 120.0) * ph
    out.append(f'<line x1="{px}" y1="{y_pm:.1f}" x2="{px+pw}" y2="{y_pm:.1f}" stroke="{t["ref_line"]}" stroke-width="1.2" stroke-dasharray="4,4"/>')
    pm_mid_x = (xs_left[0] + xs_left[1]) / 2.0
    out.append(f'<rect x="{pm_mid_x-41:.1f}" y="{y_pm-15:.1f}" width="82" height="13.5" rx="3" fill="{t["card"]}" fill-opacity="0.95"/>')
    out.append(f'<text x="{pm_mid_x:.1f}" y="{y_pm-5:.1f}" text-anchor="middle" font-family="{FONT}" font-size="9.5" font-weight="600" fill="{t["ref_line"]}">pymorphy3: 45 МБ</text>')

    # Bars for cold start ms (scaled 0..240) — slim soft fill without heavy border
    bar_w = 16
    for i, (ver, base_mb, peak_mb, ms) in enumerate(rel_left):
        cx = xs_left[i]
        bh = (ms / 240.0) * ph
        by = py + ph - bh
        out.append(
            f'<rect x="{cx-bar_w/2:.1f}" y="{by:.1f}" width="{bar_w}" height="{bh:.1f}" rx="4" '
            f'fill="{t["green"]}" fill-opacity="0.20"/>'
        )
        # X-axis label
        fw = "700" if ver == "v0.4.0" else "600"
        fc = t["accent"] if ver == "v0.4.0" else t["ink"]
        out.append(f'<text x="{cx:.1f}" y="{py+ph+17}" text-anchor="middle" font-family="{MONO}" font-size="11" font-weight="{fw}" fill="{fc}">{ver}</text>')
        out.append(f'<text x="{cx:.1f}" y="{py+ph+31}" text-anchor="middle" font-family="{MONO}" font-size="9.5" fill="{t["green"]}">{ms} мс</text>')

    # Polyline: Peak RSS
    pts_peak = " ".join(f"{xs_left[i]:.1f},{py+ph-(r[2]/120.0)*ph:.1f}" for i, r in enumerate(rel_left))
    out.append(f'<polyline fill="none" stroke="{t["blue"]}" stroke-width="2.4" points="{pts_peak}"/>')
    # Polyline: Base RSS
    pts_base = " ".join(f"{xs_left[i]:.1f},{py+ph-(r[1]/120.0)*ph:.1f}" for i, r in enumerate(rel_left))
    out.append(f'<polyline fill="none" stroke="{t["accent"]}" stroke-width="2.8" points="{pts_base}"/>')

    for i, (ver, base_mb, peak_mb, ms) in enumerate(rel_left):
        cx = xs_left[i]
        yp = py + ph - (peak_mb / 120.0) * ph
        yb = py + ph - (base_mb / 120.0) * ph
        out.append(f'<circle cx="{cx:.1f}" cy="{yp:.1f}" r="4.5" fill="{t["card"]}" stroke="{t["blue"]}" stroke-width="2.4"/>')
        out.append(f'<text x="{cx:.1f}" y="{yp-9:.1f}" text-anchor="middle" font-family="{MONO}" font-size="10.5" font-weight="700" fill="{t["blue"]}">{peak_mb:.1f}</text>')
        out.append(f'<circle cx="{cx:.1f}" cy="{yb:.1f}" r="5" fill="{t["accent"]}" stroke="{t["card"]}" stroke-width="2"/>')
        y_lbl = yb + 18 if ver == "v0.4.0" else yb - 10
        out.append(f'<rect x="{cx-17:.1f}" y="{y_lbl-10.5:.1f}" width="34" height="13" rx="3" fill="{t["card"]}" fill-opacity="0.88"/>')
        out.append(f'<text x="{cx:.1f}" y="{y_lbl:.1f}" text-anchor="middle" font-family="{MONO}" font-size="10.5" font-weight="700" fill="{t["accent"]}">{base_mb:.1f}</text>')

    # --- RIGHT CARD: Dictionary coverage & quality milestones ---
    rx, ry, rw, rh = 492, 78, 444, 266
    out.append(f'<rect x="{rx}" y="{ry}" width="{rw}" height="{rh}" rx="12" fill="{t["card"]}" stroke="{t["border"]}"/>')
    out.append(f'<text x="{rx+18}" y="{ry+26}" font-family="{FONT}" font-size="13.5" font-weight="700" fill="{t["ink"]}">Охват словарей и точность (тыс. записей)</text>')
    # Legend right card
    out.append(f'<rect x="{rx+18}" y="{ry+40}" width="10" height="10" rx="2" fill="{t["accent"]}"/>')
    out.append(f'<text x="{rx+33}" y="{ry+49}" font-family="{FONT}" font-size="11" fill="{t["muted"]}">Леммы + формы</text>')
    out.append(f'<rect x="{rx+138}" y="{ry+40}" width="10" height="10" rx="2" fill="{t["blue"]}"/>')
    out.append(f'<text x="{rx+153}" y="{ry+49}" font-family="{FONT}" font-size="11" fill="{t["muted"]}">Морфология + парадигмы</text>')
    out.append(f'<circle cx="{rx+320}" cy="{ry+45}" r="4" fill="{t["green"]}"/>')
    out.append(f'<text x="{rx+329}" y="{ry+49}" font-family="{FONT}" font-size="11" fill="{t["muted"]}">Точность RU</text>')

    # Stacked bar plot area
    qx, qy, qw, qh = rx + 42, ry + 72, rw - 64, 152
    max_k = 550.0
    for kv in (0, 150, 300, 450):
        yy = qy + qh - (kv / max_k) * qh
        out.append(f'<line x1="{qx}" y1="{yy:.1f}" x2="{qx+qw}" y2="{yy:.1f}" stroke="{t["grid"]}" stroke-width="1"/>')
        out.append(f'<text x="{qx-8}" y="{yy+3.5:.1f}" text-anchor="end" font-family="{MONO}" font-size="10" fill="{t["muted"]}">{kv}k</text>')

    # (ver, core_k = lemmas+adj_forms+types+adj_noun+ov, morph_k = morph+alt+paradigms, ru_acc, tag)
    rel_right = [
        ("v0.2.0", 249.8, 0.0, "96.2%", "старт"),
        ("v0.2.2", 288.0, 0.0, "99.2%", "PRTF"),
        ("v0.3.0", 288.2, 173.0, "99.2%", "morph+inf"),
        ("v0.3.1", 297.4, 183.9, "99.2%", "OOV-вывод"),
        ("v0.4.0", 297.4, 183.9, "99.2%", "арена"),
    ]
    xs_right = [qx + 34 + i * ((qw - 68) / (len(rel_right) - 1)) for i in range(len(rel_right))]
    bw = 38
    for i, (ver, core_k, morph_k, acc, tag) in enumerate(rel_right):
        cx = xs_right[i]
        h_core = (core_k / max_k) * qh
        h_morph = (morph_k / max_k) * qh
        y_core = qy + qh - h_core
        y_top = y_core - h_morph
        out.append(f'<rect x="{cx-bw/2:.1f}" y="{y_core:.1f}" width="{bw}" height="{h_core:.1f}" rx="3" fill="{t["accent"]}" fill-opacity="0.88"/>')
        if morph_k > 0:
            out.append(f'<rect x="{cx-bw/2:.1f}" y="{y_top:.1f}" width="{bw}" height="{h_morph:.1f}" rx="3" fill="{t["blue"]}" fill-opacity="0.88"/>')
        tot_k = int(round(core_k + morph_k))
        out.append(f'<text x="{cx:.1f}" y="{y_top-19:.1f}" text-anchor="middle" font-family="{MONO}" font-size="10" font-weight="700" fill="{t["ink"]}">{tot_k}k</text>')
        out.append(f'<text x="{cx:.1f}" y="{y_top-6:.1f}" text-anchor="middle" font-family="{MONO}" font-size="9.5" font-weight="600" fill="{t["green"]}">{acc}</text>')
        fw = "700" if ver == "v0.4.0" else "600"
        fc = t["accent"] if ver == "v0.4.0" else t["ink"]
        out.append(f'<text x="{cx:.1f}" y="{qy+qh+17}" text-anchor="middle" font-family="{MONO}" font-size="10.5" font-weight="{fw}" fill="{fc}">{ver}</text>')
        out.append(f'<text x="{cx:.1f}" y="{qy+qh+31}" text-anchor="middle" font-family="{FONT}" font-size="9.5" fill="{t["muted"]}">{esc(tag)}</text>')

    # --- BOTTOM STRIP: v0.4.0 Breakthrough KPI cards (Go / Python / WebAssembly) ---
    bx, by, bw_tot, bh_tot = 24, 358, 912, 92
    out.append(f'<rect x="{bx}" y="{by}" width="{bw_tot}" height="{bh_tot}" rx="12" fill="{t["card"]}" stroke="{t["border"]}"/>')
    out.append(f'<text x="{bx+18}" y="{by+24}" font-family="{FONT}" font-size="12.5" font-weight="700" fill="{t["ink"]}">Прорыв v0.4.0 (байтовая арена без GC-указателей + тонкий WASM)</text>')

    kpis = [
        ("Go Load(\"data\")", "187 мс → 59 мс", "3.2× быстрее · 11.3 МБ Heap", t["accent"]),
        ("Heap-объекты Go", "297k → 5.5k", "54× меньше нагрузки на GC", t["blue"]),
        ("Python (все 8 таблиц)", "93.5 → 42.3 МБ", "-51.2 МБ RAM · база 30.8 МБ", t["green"]),
        ("WASM (браузер)", "156.8 → 46.1 МБ", "3.4× меньше · 1.09 МБ gzip", t["accent"]),
    ]
    cw = 212
    gap = 12
    for i, (title, val, sub, col) in enumerate(kpis):
        kx = bx + 14 + i * (cw + gap)
        ky = by + 35
        out.append(f'<rect x="{kx}" y="{ky}" width="{cw}" height="46" rx="8" fill="{t["pill_bg"]}"/>')
        out.append(f'<text x="{kx+12}" y="{ky+15}" font-family="{FONT}" font-size="10" font-weight="600" fill="{t["muted"]}">{esc(title)}</text>')
        out.append(f'<text x="{kx+12}" y="{ky+30}" font-family="{MONO}" font-size="12.5" font-weight="700" fill="{col}">{esc(val)}</text>')
        out.append(f'<text x="{kx+12}" y="{ky+42}" font-family="{FONT}" font-size="9.5" fill="{t["muted"]}">{esc(sub)}</text>')

    out.append("</svg>")
    return "\n".join(out) + "\n"


def render_benchmark(theme_name: str, t: dict[str, str], results: dict) -> str:
    models = {m["key"]: m for m in results["models"]}
    order = [
        ("rulemma", "rulemma 0.4.0 (Go CGO)", True),
        ("pymorphy3_cached", "pymorphy3 (lru_cache 200k)", False),
        ("simplemma", "simplemma (ru)", False),
        ("snowball", "SnowballStemmer (стеммер)", False),
        ("pymystem3", "pymystem3 (Яндекс MyStem)", False),
        ("pymorphy3_uncached", "pymorphy3 (без кэша)", False),
        ("mawo_pymorphy3", "mawo-pymorphy3", False),
        ("natasha", "Natasha + Slovnet (ML)", False),
        ("steosmorphy", "SteosMorphy (Go DAWG)", False),
    ]

    W, H = 960, 446
    out: list[str] = [
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}" height="{H}" role="img" aria-label="Сравнение производительности и точности rulemma 0.4.0 с 7 библиотеками">',
        "<title>Бенчмарк rulemma 0.4.0 против 7 библиотек русского NLP (20 000 уникальных 7-словных запросов)</title>",
        f'<rect width="{W}" height="{H}" rx="14" fill="{t["bg"]}" stroke="{t["border"]}" stroke-width="1"/>',
        f'<text x="28" y="34" font-family="{FONT}" font-size="17.5" font-weight="700" fill="{t["ink"]}">Бенчмарк 8 библиотек: батч из Python (20k запросов)</text>',
        f'<text x="28" y="60" font-family="{FONT}" font-size="11.5" fill="{t["muted"]}">Отдельный процесс на библиотеку · 20 000 уникальных 7-словных запросов · Ryzen 9 5950X · Python 3.12 · Go 1.23</text>',
        f'<rect data-pill="1" x="712" y="15" width="224" height="26" rx="13" fill="{t["green_soft"]}" stroke="{t["green"]}" stroke-opacity="0.35"/>',
        f'<text x="824" y="32" text-anchor="middle" font-family="{FONT}" font-size="11.5" font-weight="700" fill="{t["green"]}">1M запросов: 1.47 с (682k/с)</text>',
    ]

    # Table headers
    bar_x = 248
    bar_max_w = 408
    col_eco = 786
    col_rss = 886
    hy = 88
    out.append(f'<text x="28" y="{hy}" font-family="{FONT}" font-size="11" font-weight="700" fill="{t["muted"]}">БИБЛИОТЕКА</text>')
    out.append(f'<text x="{bar_x}" y="{hy}" font-family="{FONT}" font-size="11" font-weight="700" fill="{t["muted"]}">СКОРОСТЬ БАТЧА (запросов / сек)</text>')
    out.append(f'<text x="{col_eco}" y="{hy}" text-anchor="middle" font-family="{FONT}" font-size="11" font-weight="700" fill="{t["muted"]}">E-COM</text>')
    out.append(f'<text x="{col_rss}" y="{hy}" text-anchor="middle" font-family="{FONT}" font-size="11" font-weight="700" fill="{t["muted"]}">ПИК RSS</text>')
    out.append(f'<line x1="24" y1="{hy+10}" x2="{W-24}" y2="{hy+10}" stroke="{t["border"]}" stroke-width="1"/>')

    max_qps = max(models[k]["batch_qps"] for k, _, _ in order if k in models)
    row_h = 34
    start_y = hy + 20

    for idx, (key, label, is_main) in enumerate(order):
        m = models.get(key)
        if not m:
            continue
        qps = int(m["batch_qps"])
        eco = float(m["ecom_acc"])
        rss = float(m["peak_rss_mb"])
        y = start_y + idx * row_h

        if is_main:
            out.append(f'<rect x="20" y="{y-4}" width="{W-40}" height="{row_h-2}" rx="8" fill="{t["accent_soft"]}"/>')
        elif idx % 2 == 1:
            out.append(f'<rect x="20" y="{y-4}" width="{W-40}" height="{row_h-2}" rx="6" fill="{t["card"]}" fill-opacity="0.55"/>')

        name_col = t["accent"] if is_main else t["ink"]
        fw = "700" if is_main else "500"
        out.append(f'<text x="30" y="{y+17}" font-family="{FONT}" font-size="12" font-weight="{fw}" fill="{name_col}">{esc(label)}</text>')

        # Bar track
        out.append(f'<rect x="{bar_x}" y="{y+4}" width="{bar_max_w}" height="18" rx="4" fill="{t["grid"]}"/>')
        bw = max(5.0, (qps / max_qps) * bar_max_w)
        bcol = t["accent"] if is_main else (t["blue"] if qps > 100000 else t["bar_other_hi"])
        out.append(f'<rect x="{bar_x}" y="{y+4}" width="{bw:.1f}" height="18" rx="4" fill="{bcol}"/>')

        # QPS label
        qps_txt = f"{fmt_int(qps)} /с"
        if bw > 110:
            out.append(f'<text x="{bar_x+bw-8:.1f}" y="{y+16.5}" text-anchor="end" font-family="{MONO}" font-size="10.5" font-weight="700" fill="#FFFFFF">{qps_txt}</text>')
        else:
            out.append(f'<text x="{bar_x+bw+8:.1f}" y="{y+16.5}" text-anchor="start" font-family="{MONO}" font-size="10.5" font-weight="600" fill="{t["ink"]}">{qps_txt}</text>')

        # E-Com accuracy pill
        eco_bg = t["green_soft"] if eco >= 99.0 else t["pill_bg"]
        eco_fg = t["green"] if eco >= 99.0 else (t["ink"] if eco >= 80.0 else t["muted"])
        out.append(f'<rect x="{col_eco-34}" y="{y+2}" width="68" height="22" rx="11" fill="{eco_bg}"/>')
        out.append(f'<text x="{col_eco}" y="{y+16.5}" text-anchor="middle" font-family="{MONO}" font-size="10.5" font-weight="700" fill="{eco_fg}">{eco:.1f}%</text>')

        # Peak RSS
        rss_txt = f"{rss:.1f} МБ" + ("*" if is_main else ("†" if key == "pymystem3" else ""))
        rss_fg = t["accent"] if is_main else (t["muted"] if rss > 200 else t["ink"])
        out.append(f'<text x="{col_rss}" y="{y+16.5}" text-anchor="middle" font-family="{MONO}" font-size="10.5" font-weight="{fw}" fill="{rss_fg}">{rss_txt}</text>')

    # Footer footnote
    fy = H - 16
    ru = models["rulemma"]
    out.append(
        f'<text x="28" y="{fy}" font-family="{FONT}" font-size="10.5" fill="{t["muted"]}">'
        f'*База RSS rulemma 0.4.0 после загрузки: {ru["load_rss_mb"]:.1f} МБ (ниже pymorphy3 {models["pymorphy3_cached"]["load_rss_mb"]:.1f} МБ); '
        f'†pymystem3: только Python-обёртка без дочернего C++ процесса mystem.</text>'
    )
    out.append("</svg>")
    return "\n".join(out) + "\n"


def render_pipeline(theme_name: str, t: dict[str, str]) -> str:
    W, H = 960, 318
    out: list[str] = [
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}" height="{H}" role="img" aria-label="Архитектура конвейера rulemma">',
        "<title>Архитектура конвейера rulemma: от сырого запроса до Go, Python, CLI и WASM</title>",
        f'<rect width="{W}" height="{H}" rx="14" fill="{t["bg"]}" stroke="{t["border"]}" stroke-width="1"/>',
        # Header
        f'<text x="28" y="34" font-family="{FONT}" font-size="17.5" font-weight="700" fill="{t["ink"]}">Архитектура конвейера rulemma</text>',
        f'<text x="28" y="58" font-family="{FONT}" font-size="11.5" fill="{t["muted"]}">От сырого запроса к каноническому ключу, морфологии и склонению без внешних зависимостей и GC-пауз</text>',
        f'<rect data-pill="1" x="724" y="15" width="212" height="26" rx="13" fill="{t["blue_soft"]}" stroke="{t["blue"]}" stroke-opacity="0.35"/>',
        f'<text x="830" y="32" text-anchor="middle" font-family="{FONT}" font-size="11.5" font-weight="700" fill="{t["blue"]}">0 зависимостей · 0 GC pointers</text>',
    ]

    # 4 calm horizontal cards with arrows between them
    cy, ch = 80, 152
    cw = 204
    gap = 32
    xs = [24 + i * (cw + gap) for i in range(4)]

    stages = [
        (
            "01 · ВХОД И ОЧИСТКА",
            "Нормализация",
            t["muted"],
            t["pill_bg"],
            [
                ("Омоглифы RU/EN", "cм → см, intеl → intel"),
                ("Токенизатор", "default или technical"),
                ("Характеристики", "2,6 л.с. · 100×100 · б/у"),
            ],
        ),
        (
            "02 · ПОИСК ЛЕММЫ",
            "Байтовая арена",
            t["accent"],
            t["accent_soft"],
            [
                ("1. overrides.tsv", "469 правил (приоритет)"),
                ("2. compactStrMap", "214 196 форм · maphash"),
                ("3. OOV fallback", "проверка по словарю"),
            ],
        ),
        (
            "03 · ГРАММАТИКА",
            "Ленивые таблицы",
            t["blue"],
            t["blue_soft"],
            [
                ("adj_noun", "3 960 пар прил. → сущ."),
                ("morph + омонимы", "127k форм + 11.9k разб."),
                ("paradigms + OOV", "44.8k лемм · atomic"),
            ],
        ),
        (
            "04 · РЕЗУЛЬТАТ",
            "Единое API",
            t["green"],
            t["green_soft"],
            [
                ("Ключи и дедуп", "Word · LemmaKey · SemKey"),
                ("Морфология", "Analyze · PrepGoverns"),
                ("Генерация", "InflectPhrase · SEOName"),
            ],
        ),
    ]

    for idx, (step_lbl, heading, col, soft, rows) in enumerate(stages):
        x = xs[idx]
        # Card shell
        out.append(f'<rect x="{x}" y="{cy}" width="{cw}" height="{ch}" rx="11" fill="{t["card"]}" stroke="{t["border"]}" stroke-width="1"/>')
        # Soft top strip
        out.append(f'<rect x="{x+1}" y="{cy+1}" width="{cw-2}" height="32" rx="10" fill="{soft}"/>')
        out.append(f'<rect x="{x+1}" y="{cy+22}" width="{cw-2}" height="11" fill="{soft}"/>')
        out.append(f'<line x1="{x}" y1="{cy+33}" x2="{x+cw}" y2="{cy+33}" stroke="{t["border"]}" stroke-width="0.8"/>')
        out.append(f'<text x="{x+12}" y="{cy+21}" font-family="{MONO}" font-size="10" font-weight="700" fill="{col}">{esc(step_lbl)}</text>')
        out.append(f'<text x="{x+12}" y="{cy+52}" font-family="{FONT}" font-size="13" font-weight="700" fill="{t["ink"]}">{esc(heading)}</text>')

        ry = cy + 72
        for k, v in rows:
            out.append(f'<circle cx="{x+16}" cy="{ry-3}" r="2.3" fill="{col}"/>')
            out.append(f'<text x="{x+25}" y="{ry}" font-family="{FONT}" font-size="10.8" font-weight="600" fill="{t["ink"]}">{esc(k)}</text>')
            out.append(f'<text x="{x+25}" y="{ry+13}" font-family="{MONO}" font-size="9.5" fill="{t["muted"]}">{esc(v)}</text>')
            ry += 28

        # Arrow to next card
        if idx < 3:
            ax1 = x + cw + 4
            ax2 = xs[idx + 1] - 6
            ay = cy + ch / 2.0
            out.append(f'<line x1="{ax1}" y1="{ay:.1f}" x2="{ax2-4}" y2="{ay:.1f}" stroke="{t["ref_line"]}" stroke-width="1.6"/>')
            out.append(f'<polygon points="{ax2},{ay:.1f} {ax2-7},{ay-4.5:.1f} {ax2-7},{ay+4.5:.1f}" fill="{t["ref_line"]}"/>')

    # Bottom strip: 3 runtime targets
    by, bh = 250, 48
    targets = [
        ("Go stdlib (нативно)", "59 мс старт · 15.6 МБ RSS · 3.6 мкс/запр", t["blue"]),
        ("Python ctypes / CGO", "682k запр/с · 37.1 МБ RSS · без GIL", t["accent"]),
        ("CLI & WebAssembly", "TSV до 1.6M/с · WASM 1.09 МБ gzip", t["green"]),
    ]
    tw = 294
    tgap = 15
    for i, (name, meta, col) in enumerate(targets):
        tx = 24 + i * (tw + tgap)
        out.append(f'<rect x="{tx}" y="{by}" width="{tw}" height="{bh}" rx="9" fill="{t["card"]}" stroke="{t["border"]}" stroke-width="1"/>')
        out.append(f'<rect x="{tx}" y="{by+9}" width="3.5" height="{bh-18}" rx="2" fill="{col}"/>')
        out.append(f'<text x="{tx+14}" y="{by+21}" font-family="{FONT}" font-size="11.5" font-weight="700" fill="{col}">{esc(name)}</text>')
        out.append(f'<text x="{tx+14}" y="{by+38}" font-family="{MONO}" font-size="9.8" fill="{t["muted"]}">{esc(meta)}</text>')

    out.append("</svg>")
    return "\n".join(out) + "\n"


def main() -> int:
    ASSETS.mkdir(parents=True, exist_ok=True)
    results = json.loads(RESULTS_PATH.read_text(encoding="utf-8"))
    for name, palette in THEMES.items():
        (ASSETS / f"version-evolution-{name}.svg").write_text(
            render_version_evolution(name, palette), encoding="utf-8"
        )
        (ASSETS / f"benchmark-{name}.svg").write_text(
            render_benchmark(name, palette, results), encoding="utf-8"
        )
        (ASSETS / f"pipeline-{name}.svg").write_text(
            render_pipeline(name, palette), encoding="utf-8"
        )
    print(f"[charts] Generated 6 Light/Dark SVG charts in {ASSETS}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
