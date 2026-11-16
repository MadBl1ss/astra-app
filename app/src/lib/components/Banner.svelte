<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';

  let {
    tone,
    title,
    body = '',
    actions,
  }: {
    tone: 'warn' | 'bad';
    title: string;
    body?: string;
    actions?: Snippet;
  } = $props();
</script>

<!-- The colour never speaks alone: the icon and the words say the same. -->
<div class="banner {tone}" role="status">
  <span class="ico"><Icon name={tone === 'bad' ? 'stop' : 'alert'} /></span>
  <div class="txt">
    <b>{title}</b>
    {#if body}<span title={body}>{body}</span>{/if}
  </div>
  {#if actions}<div class="acts">{@render actions()}</div>{/if}
</div>

<style>
  .banner {
    --tone: var(--warn);
    display: flex;
    align-items: center;
    gap: var(--s3);
    padding: 9px 12px;
    border: 1px solid color-mix(in srgb, var(--tone) 45%, var(--line));
    border-radius: var(--r-card);
    background: color-mix(in srgb, var(--tone) 9%, var(--surface));
    animation: appear var(--slow) ease-out;
  }

  .bad {
    --tone: var(--bad);
  }

  @keyframes appear {
    from {
      opacity: 0;
      transform: translateY(-4px);
    }
  }

  .ico {
    display: flex;
    color: var(--tone);
  }

  .txt {
    flex: 1;
    min-width: 0;
  }

  .txt b {
    display: block;
    font-weight: 600;
  }

  /* Two lines at most; the rest is in the tooltip and in the journal. */
  .txt span {
    display: -webkit-box;
    overflow: hidden;
    color: var(--muted);
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
    line-clamp: 2;
  }

  .acts {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: 6px;
  }
</style>
