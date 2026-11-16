<script lang="ts" module>
  let seq = 0;
</script>

<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';

  // One setting: what it is and what it does on the left, the control on the
  // right, and the daemon's refusal under it when there was one. The
  // control gets the label's id, so it is announced by name.
  let {
    label,
    note,
    error = null,
    warn = null,
    wide = false,
    children,
  }: {
    label: string;
    note?: string;
    error?: string | null;
    /** A caution that is not an error: "the lock does not protect yet". */
    warn?: string | null;
    /** The control takes the whole row under the text (a long choice). */
    wide?: boolean;
    children: Snippet<[labelledby: string]>;
  } = $props();

  const id = `setting-${++seq}`;
</script>

<div class="row" class:wide>
  <div class="text">
    <span class="label" {id}>{label}</span>
    {#if note}<p class="note">{note}</p>{/if}
    {#if warn}<p class="warn"><Icon name="alert" size={13} />{warn}</p>{/if}
    {#if error}<p class="err" role="alert"><Icon name="alert" size={13} />{error}</p>{/if}
  </div>
  <div class="ctl">{@render children(id)}</div>
</div>

<style>
  .row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    align-items: center;
    gap: var(--s2) var(--s5);
    padding: 11px 14px;
  }

  .row + :global(.row) {
    border-top: 1px solid var(--line);
  }

  .wide {
    grid-template-columns: minmax(0, 1fr);
  }

  .label {
    display: block;
  }

  .note,
  .warn,
  .err {
    margin-top: 2px;
    color: var(--muted);
    font-size: var(--fs-cap);
    line-height: 1.4;
  }

  .warn,
  .err {
    display: flex;
    align-items: flex-start;
    gap: 5px;
  }

  .warn {
    color: var(--warn);
  }

  .err {
    color: var(--bad);
  }

  .warn :global(svg),
  .err :global(svg) {
    margin-top: 1px;
  }

  .ctl {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: var(--s2);
    min-width: 0;
  }

  .wide .ctl {
    justify-content: flex-start;
  }
</style>
