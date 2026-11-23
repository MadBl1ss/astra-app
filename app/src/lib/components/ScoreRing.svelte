<script lang="ts">
  import type { ServerStats } from '../proto';
  import { tierOf, toneOf } from '../rating';

  // A server's score as a ring that fills up to it, the number inside. The
  // ring is the colour of the score's range; a score with little evidence
  // behind it is grey on a dashed track, with "?" for a number, since it is
  // mostly a guess. The arc grows from nothing when the ring appears and
  // slides when the score moves.
  let {
    stats,
    size = 22,
    label,
  }: {
    stats: ServerStats | null;
    size?: number;
    /** What a reader hears; the picture says the same. */
    label: string;
  } = $props();

  const tier = $derived(tierOf(stats));
  const tone = $derived(toneOf(tier));
  const shown = $derived(stats !== null && tier !== 'none');
  // The ring is drawn on a 24-unit box, whatever its size on screen.
  const R = 10;
  const C = 2 * Math.PI * R;
  let grown = $state(false);
  $effect(() => {
    // One frame at zero first, so that the arc has something to grow from.
    const frame = requestAnimationFrame(() => (grown = true));
    return () => cancelAnimationFrame(frame);
  });
  const share = $derived(grown && shown ? Math.max(0.04, stats!.score / 100) : 0);
  const big = $derived(size >= 36);
</script>

<span class="ring {tone}" class:new={tier === 'new'} class:big style:width="{size}px" style:height="{size}px" role="img" aria-label={label}>
  <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true">
    <circle class="track" cx="12" cy="12" r={R} />
    {#if shown}
      <circle class="arc" cx="12" cy="12" r={R} stroke-dasharray={C} stroke-dashoffset={C * (1 - share)} />
    {/if}
  </svg>
  <span class="num" aria-hidden="true">{shown ? (tier === 'new' ? '?' : stats!.score) : '—'}</span>
</span>

<style>
  .ring {
    position: relative;
    display: inline-grid;
    flex: none;
    place-items: center;
  }

  svg {
    position: absolute;
    inset: 0;
    transform: rotate(-90deg);
  }

  circle {
    fill: none;
    stroke-width: 2.4;
  }

  .big circle {
    stroke-width: 1.8;
  }

  .track {
    stroke: var(--line);
  }

  .arc {
    stroke: var(--muted);
    stroke-linecap: round;
    transition:
      stroke-dashoffset 0.9s cubic-bezier(0.22, 1, 0.36, 1),
      stroke 0.3s;
  }

  .ok .arc {
    stroke: var(--vpn);
  }

  .warn .arc {
    stroke: var(--warn);
  }

  .bad .arc {
    stroke: var(--bad);
  }

  .new .track {
    stroke-dasharray: 2 2.6;
  }

  .num {
    position: relative;
    color: var(--text);
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: -0.02em;
    font-variant-numeric: tabular-nums;
    line-height: 1;
  }

  .big .num {
    font: 600 15px / 1 var(--font-display);
  }

  .new .num,
  .none .num {
    color: var(--muted);
  }
</style>
