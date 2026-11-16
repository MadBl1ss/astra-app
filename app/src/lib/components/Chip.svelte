<script lang="ts">
  import Icon from './Icon.svelte';

  // One rule as a plate: the value, then "move to the other list" and
  // "remove". Both buttons say what they do in their names and tooltips.
  let {
    label,
    tag,
    tagTitle,
    fresh = false,
    moveLabel,
    removeLabel,
    onmove,
    onremove,
  }: {
    label: string;
    /** A quiet word after the value, e.g. "exactly" for a domain without subdomains. */
    tag?: string;
    tagTitle?: string;
    /** Just added: drawn out for a moment so the eye finds it. */
    fresh?: boolean;
    moveLabel?: string;
    removeLabel: string;
    onmove?: () => void;
    onremove: () => void;
  } = $props();
</script>

<li class="chip" class:fresh title={tagTitle ?? label}>
  <span class="txt">{label}</span>
  {#if tag}<span class="tag">{tag}</span>{/if}
  {#if onmove && moveLabel}
    <button type="button" aria-label={moveLabel} title={moveLabel} onclick={onmove}><Icon name="swap" size={13} /></button>
  {/if}
  <button type="button" aria-label={removeLabel} title={removeLabel} onclick={onremove}><Icon name="close" size={13} /></button>
</li>

<style>
  .chip {
    display: inline-flex;
    align-items: center;
    max-width: 100%;
    min-height: 26px;
    padding: 0 2px 0 9px;
    border: 1px solid var(--line);
    border-radius: var(--r-ctl);
    background: var(--surface-2);
  }

  .fresh {
    border-color: var(--muted);
    animation: glow 1.6s ease-out;
  }

  @keyframes glow {
    from {
      background: color-mix(in srgb, var(--text) 16%, var(--surface-2));
    }
  }

  .txt {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .tag {
    flex: none;
    margin-left: 5px;
    color: var(--muted);
    font-size: 11px;
  }

  button {
    display: grid;
    flex: none;
    place-items: center;
    width: 22px;
    height: 22px;
    margin-left: 1px;
    padding: 0;
    border: 0;
    border-radius: 4px;
    background: transparent;
    color: var(--muted);
  }

  button:hover {
    background: var(--surface);
    color: var(--text);
  }

  button:focus-visible {
    outline-offset: -1px;
  }
</style>
