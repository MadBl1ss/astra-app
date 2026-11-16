<script lang="ts">
  // A list that is on its way: a line of pale rows in its shape, so that the
  // screen does not jump when the answer comes, and a word for screen readers.
  let { rows = 4, label }: { rows?: number; label: string } = $props();
</script>

<div class="loading" role="status" aria-label={label}>
  {#each { length: rows } as _, i (i)}
    <span class="bar" style:width="{88 - ((i * 23) % 40)}%"></span>
  {/each}
</div>

<style>
  .loading {
    display: grid;
    gap: 14px;
    padding: 14px 4px;
  }

  .bar {
    height: 10px;
    border-radius: 5px;
    background: var(--surface-2);
    animation: breathe 1.4s ease-in-out infinite;
  }

  @keyframes breathe {
    50% {
      opacity: 0.45;
    }
  }
</style>
