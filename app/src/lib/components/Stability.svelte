<script lang="ts">
  import { ru } from '../i18n/ru';
  import type { Server } from '../proto';

  // How often a server answered: five cells, filled by the share of answers
  // in its recorded probes. The words are in the tooltip and for readers.
  let { stats }: { stats: Server['stats'] } = $props();

  const CELLS = 5;
  const known = $derived(stats !== null && stats.samples > 0);
  const share = $derived(known ? 1 - stats!.loss : 0);
  const filled = $derived(known ? Math.max(1, Math.round(share * CELLS)) : 0);
  const tone = $derived(!known ? 'none' : stats!.loss < 0.05 ? 'ok' : stats!.loss < 0.2 ? 'warn' : 'bad');
  const text = $derived(known ? ru.servers.stability(Math.round(share * 100), stats!.avg, stats!.samples) : ru.servers.noStats);
</script>

<span class="meter {tone}" role="img" aria-label={text} title={text}>
  {#each { length: CELLS } as _, i (i)}
    <span class="cell" class:on={i < filled}></span>
  {/each}
</span>

<style>
  .meter {
    display: inline-flex;
    flex: none;
    gap: 2px;
    align-items: center;
  }

  .cell {
    width: 4px;
    height: 12px;
    border-radius: 1px;
    background: var(--line);
  }

  .ok .on {
    background: var(--vpn);
  }

  .warn .on {
    background: var(--warn);
  }

  .bad .on {
    background: var(--bad);
  }
</style>
