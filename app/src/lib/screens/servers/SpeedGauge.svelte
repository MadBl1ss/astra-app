<script lang="ts">
  import type { Snippet } from 'svelte';
  import { dialAt, follow, TICKS } from '../../rating';

  // A speedometer: a dial of 240 degrees with marks that go up by factors, a
  // needle that follows the speed smoothly whatever the screen's frame rate,
  // and the stretch up to the needle lit with the colour of a working VPN.
  // What it reads in words sits under the dial, between the ends of its arc,
  // where the needle never goes (children).
  let {
    value,
    live = false,
    children,
  }: {
    /** Mbit/s the needle heads for. */
    value: number;
    /** A download is under way: the lit stretch glows. */
    live?: boolean;
    children?: Snippet;
  } = $props();

  const W = 280;
  const H = 196;
  const CX = W / 2;
  const CY = 136;
  const R = 110;
  const START = 150; // degrees, clockwise from the right, as SVG draws
  const SWEEP = 240;

  function point(deg: number, r: number): [number, number] {
    const a = (deg * Math.PI) / 180;
    return [CX + r * Math.cos(a), CY + r * Math.sin(a)];
  }

  function arc(from: number, to: number, r: number): string {
    if (to - from < 1) return ''; // a round cap alone would be a stray dot at zero
    const [x1, y1] = point(from, r);
    const [x2, y2] = point(to, r);
    return `M${x1.toFixed(2)} ${y1.toFixed(2)}A${r} ${r} 0 ${to - from > 180 ? 1 : 0} 1 ${x2.toFixed(2)} ${y2.toFixed(2)}`;
  }

  const marks = TICKS.map((v) => {
    const deg = START + dialAt(v) * SWEEP;
    const [x1, y1] = point(deg, R - 13);
    const [x2, y2] = point(deg, R - 7);
    const [lx, ly] = point(deg, R - 26);
    return { v, x1, y1, x2, y2, lx, ly, label: v >= 1000 ? '1G' : String(v) };
  });
  // Small marks halfway between the big ones.
  const minor = TICKS.slice(1).map((_, i) => {
    const deg = START + ((i + 0.5) / (TICKS.length - 1)) * SWEEP;
    const [x1, y1] = point(deg, R - 11);
    const [x2, y2] = point(deg, R - 7);
    return { x1, y1, x2, y2 };
  });

  // The needle's own position, 0..1, chasing the value frame by frame.
  let shown = $state(0);
  const still = typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
  $effect(() => {
    const target = dialAt(value);
    if (still) {
      shown = target;
      return;
    }
    let frame = 0;
    let last = performance.now();
    const step = (t: number) => {
      shown = follow(shown, target, t - last, 140);
      last = t;
      if (shown !== target) frame = requestAnimationFrame(step);
    };
    frame = requestAnimationFrame(step);
    return () => cancelAnimationFrame(frame);
  });

  const angle = $derived(START + shown * SWEEP);
  const lit = $derived(arc(START, angle, R));
  const tip = $derived(point(angle, R - 30));
  const base1 = $derived(point(angle + 90, 3.2));
  const base2 = $derived(point(angle - 90, 3.2));
  const tail = $derived(point(angle + 180, 12));
</script>

<div class="gauge" class:live>
  <svg viewBox="0 0 {W} {H}" width={W} height={H} aria-hidden="true">
    <defs>
      <!-- Across the dial, not along the lit stretch: a colour means a speed. -->
      <linearGradient id="gauge-lit" gradientUnits="userSpaceOnUse" x1={CX - R} y1="0" x2={CX + R} y2="0">
        <stop offset="0" class="stop-a" />
        <stop offset="1" class="stop-b" />
      </linearGradient>
    </defs>
    <path class="track" d={arc(START, START + SWEEP, R)} />
    {#if lit}<path class="lit" d={lit} stroke="url(#gauge-lit)" />{/if}
    {#each minor as m, i (i)}
      <line class="minor" x1={m.x1} y1={m.y1} x2={m.x2} y2={m.y2} />
    {/each}
    {#each marks as m (m.v)}
      <line class="mark" class:passed={dialAt(m.v) <= shown + 1e-6} x1={m.x1} y1={m.y1} x2={m.x2} y2={m.y2} />
      <text class="label" class:passed={dialAt(m.v) <= shown + 1e-6} x={m.lx} y={m.ly}>{m.label}</text>
    {/each}
    <polygon class="needle" points="{tip[0]},{tip[1]} {base1[0]},{base1[1]} {tail[0]},{tail[1]} {base2[0]},{base2[1]}" />
    <circle class="hub" cx={CX} cy={CY} r="6.5" />
  </svg>
  <div class="readout">
    {@render children?.()}
  </div>
</div>

<style>
  .gauge {
    position: relative;
    width: 280px;
    height: 232px;
  }

  svg {
    display: block;
    overflow: visible;
  }

  .stop-a {
    stop-color: var(--globe-ok-a);
  }

  .stop-b {
    stop-color: var(--globe-ok-b);
  }

  .track,
  .lit {
    fill: none;
    stroke-width: 10;
    stroke-linecap: round;
  }

  .track {
    stroke: var(--surface-2);
  }

  .lit {
    transition: filter 0.4s;
  }

  .live .lit {
    filter: drop-shadow(0 0 6px color-mix(in srgb, var(--globe-ok-a) 55%, transparent));
  }

  .mark,
  .minor {
    stroke: var(--line);
    stroke-width: 1.6;
    stroke-linecap: round;
  }

  .minor {
    stroke-width: 1;
  }

  .mark.passed {
    stroke: var(--muted);
  }

  .label {
    fill: var(--muted);
    font-size: 10px;
    font-variant-numeric: tabular-nums;
    text-anchor: middle;
    dominant-baseline: middle;
    opacity: 0.7;
    transition: opacity 0.3s;
  }

  .label.passed {
    fill: var(--text);
    opacity: 1;
  }

  .needle {
    fill: var(--text);
  }

  .hub {
    fill: var(--surface);
    stroke: var(--text);
    stroke-width: 2.5;
  }

  .readout {
    position: absolute;
    top: 178px;
    right: 0;
    left: 0;
    display: grid;
    justify-items: center;
    gap: 2px;
    text-align: center;
  }
</style>
