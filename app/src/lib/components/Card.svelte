<script lang="ts">
  import type { Snippet } from 'svelte';

  let { caption, flush = false, children }: { caption?: string; flush?: boolean; children: Snippet } = $props();
</script>

<section class="card" class:flush>
  {#if caption}<h3 class="cap">{caption}</h3>{/if}
  {@render children()}
</section>

<style>
  .card {
    min-width: 0;
    padding: 9px 12px 11px;
    border: 1px solid var(--line);
    border-radius: var(--r-card);
    background: var(--surface);
  }

  .flush {
    padding: 6px 8px 2px;
  }

  .cap {
    margin-bottom: 6px;
    color: var(--muted);
    font-size: var(--fs-cap);
    font-weight: 400;
  }
</style>
