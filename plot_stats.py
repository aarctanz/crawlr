#!/usr/bin/env python3
"""Render crawler time-series charts from a stats.json file, using matplotlib.

Produces five PNGs next to the input:
    queued_vs_claimed.png   links discovered vs picked up by workers
    crawled_vs_success.png  pages completed vs fetched successfully
    errors.png              total failures vs HTTP 429s
    goroutines.png          goroutine count over the run
    heap_mb.png             heap allocation over the run

For the comparison charts the area between the two lines is shaded, because that
gap is the quantity worth reading: unclaimed backlog, failed pages, non-429
failures respectively. Each chart carries a two-line run summary in the footer.

Usage:
    .venv/bin/python plot_stats.py [STATS_JSON_OR_DIR] [-o OUTPUT_DIR]

    .venv/bin/python plot_stats.py                  # ./stats.json -> ./
    .venv/bin/python plot_stats.py data/sequential  # a saved run
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.ticker import MaxNLocator

# Typography and the greys used for chrome.
INK = "#16213e"     # titles
MUTED = "#5c6b7a"   # subtitle, tick labels
FAINT = "#98a0ab"   # footer
GRID = "#e6e8ec"    # horizontal gridlines
SPINE = "#d4d8de"   # the two spines we keep

plt.rcParams.update(
    {
        "font.family": "sans-serif",
        "font.sans-serif": ["Inter", "DejaVu Sans", "Helvetica", "Arial"],
        "figure.facecolor": "white",
        "axes.facecolor": "white",
    }
)

# Colours per series.
INDIGO, TEAL = "#3b34c4", "#1098ad"
VIOLET, GREEN = "#7048e8", "#2f9e44"
RED, AMBER = "#e03131", "#f08c00"
ORANGE = "#f06511"


def load_samples(stats_path: Path) -> list[dict]:
    """Read stats.json and normalise it to a list of sample dicts.

    Tolerates the current schema and the older archived one (Sample /
    PagesTotal / ClaimedTotal). Fields absent in older runs default to 0.
    """
    data = json.loads(stats_path.read_text())
    raw = data.get("TimeSeriesSamples") or data.get("Sample") or []
    samples = []
    for s in raw:
        samples.append(
            {
                "elapsed": s["ElapsedS"],
                "queued": s.get("Queued", 0),
                "claimed": s.get("Claimed", s.get("ClaimedTotal", 0)),
                "crawled": s.get("Crawled", s.get("PagesTotal", 0)),
                "success": s.get("Success", 0),
                "http429": s.get("HTTP429", 0),
                "active": s.get("ActiveWorkers", 0),
                "heap": s.get("HeapMB", 0.0),
                "goroutines": s.get("Goroutines", 0),
            }
        )
    return samples


def format_runtime(seconds: int) -> str:
    minutes, secs = divmod(int(seconds), 60)
    return f"{minutes}m {secs}s" if minutes else f"{secs}s"


def footer_text(samples: list[dict]) -> str:
    total_pages = max(s["crawled"] for s in samples)
    total_success = max(s["success"] for s in samples)
    runtime_s = max(s["elapsed"] for s in samples)
    peak_heap = max(s["heap"] for s in samples)
    peak_goroutines = max(s["goroutines"] for s in samples)
    failures = total_pages - total_success
    rate = total_pages / (runtime_s / 60) if runtime_s else 0.0
    return (
        f"Total crawled: {total_pages} · Success: {total_success} · "
        f"Failed: {failures} · Runtime: {format_runtime(runtime_s)}\n"
        f"Peak heap: {peak_heap:.2f} MB · Peak goroutines: {peak_goroutines} · "
        f"Avg crawl rate: {rate:.1f} pages/min"
    )


def style_axes(ax, *, integer_y: bool) -> None:
    ax.spines["top"].set_visible(False)
    ax.spines["right"].set_visible(False)
    for side in ("left", "bottom"):
        ax.spines[side].set_color(SPINE)
    ax.tick_params(colors=MUTED, labelsize=11, length=0)
    ax.grid(axis="y", color=GRID, linewidth=1)
    ax.set_axisbelow(True)
    ax.margins(x=0.01)
    ax.set_xlim(left=0)
    ax.set_ylim(bottom=0)
    if integer_y:
        ax.yaxis.set_major_locator(MaxNLocator(integer=True))


def make_chart(
    dest: Path,
    x: list,
    series: list[tuple[str, list, str]],
    *,
    title: str,
    subtitle: str,
    ylabel: str,
    footer: str,
    shade_gap: bool = False,
    step: bool = False,
    integer_y: bool = True,
) -> None:
    fig, ax = plt.subplots(figsize=(11.4, 6.6), dpi=150)
    fig.subplots_adjust(top=0.80, bottom=0.18, left=0.08, right=0.96)

    drawstyle = "steps-post" if step else "default"
    for label, y, colour in series:
        ax.plot(
            x, y,
            color=colour, linewidth=2.3, label=label,
            marker="" if step else "o", markersize=4, drawstyle=drawstyle,
        )

    if shade_gap and len(series) >= 2:
        upper, lower = series[0][1], series[1][1]
        ax.fill_between(x, lower, upper, color=series[0][2], alpha=0.12,
                        step="post" if step else None)
    elif len(series) == 1:
        ax.fill_between(x, 0, series[0][1], color=series[0][2], alpha=0.12,
                        step="post" if step else None)

    style_axes(ax, integer_y=integer_y)
    ax.set_xlabel("Seconds", fontsize=13, color=INK)
    ax.set_ylabel(ylabel, fontsize=13, color=INK)
    if len(series) > 1:
        ax.legend(frameon=False, loc="upper left", fontsize=11, labelcolor=INK)

    fig.text(0.08, 0.93, f"Crawler Stats — {title}", fontsize=21,
             fontweight="bold", color=INK)
    fig.text(0.08, 0.87, subtitle, fontsize=12.5, fontweight="bold", color=MUTED)
    fig.text(0.08, 0.035, footer, fontsize=9.5, color=FAINT,
             linespacing=1.6, va="bottom")

    fig.savefig(dest)
    plt.close(fig)
    print(f"wrote {dest}")


def resolve_paths(target: str, out: str | None) -> tuple[Path, Path]:
    p = Path(target)
    stats_path = p / "stats.json" if p.is_dir() else p
    if not stats_path.is_file():
        sys.exit(f"error: no stats.json at {stats_path}")
    out_dir = Path(out) if out else stats_path.parent
    out_dir.mkdir(parents=True, exist_ok=True)
    return stats_path, out_dir


def main() -> None:
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument(
        "stats", nargs="?", default="stats.json",
        help="path to stats.json, or a directory containing one (default: stats.json)",
    )
    parser.add_argument("-o", "--out", help="output directory (default: alongside stats.json)")
    args = parser.parse_args()

    stats_path, out_dir = resolve_paths(args.stats, args.out)
    samples = load_samples(stats_path)
    if not samples:
        sys.exit(f"error: {stats_path} has no samples")

    footer = footer_text(samples)
    x = [s["elapsed"] for s in samples]
    col = lambda key: [s[key] for s in samples]
    failed = [s["crawled"] - s["success"] for s in samples]

    make_chart(
        out_dir / "queued_vs_claimed.png", x,
        [("Queued", col("queued"), INDIGO), ("Claimed", col("claimed"), TEAL)],
        title="Queue", subtitle="Links discovered vs picked up by workers",
        ylabel="URLs", footer=footer, shade_gap=True,
    )
    make_chart(
        out_dir / "outcomes.png", x,
        [
            ("Crawled", col("crawled"), INDIGO),
            ("Success", col("success"), GREEN),
            ("Failed", failed, RED),
            ("HTTP 429", col("http429"), AMBER),
        ],
        title="Outcomes",
        subtitle="Pages crawled, succeeded, failed, and rate-limited",
        ylabel="Pages", footer=footer,
    )
    make_chart(
        out_dir / "goroutines.png", x,
        [
            ("Goroutines", col("goroutines"), ORANGE),
            ("Active workers", col("active"), TEAL),
        ],
        title="Goroutines", subtitle="Goroutine count and active workers during execution",
        ylabel="Count", footer=footer, step=True,
    )
    make_chart(
        out_dir / "heap_mb.png", x,
        [("Heap", col("heap"), VIOLET)],
        title="Heap Memory", subtitle="Heap allocation across the crawl",
        ylabel="Heap memory (MB)", footer=footer, integer_y=False,
    )


if __name__ == "__main__":
    main()
