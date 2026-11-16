<script lang="ts" module>
  let seq = 0;
</script>

<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon, { type IconName } from './Icon.svelte';

  // A text field with its label and, under it, either the daemon's objection
  // or a quiet hint. The objection is read out with the field.
  let {
    value = $bindable(''),
    input = $bindable(),
    label,
    hideLabel = false,
    placeholder = '',
    error = null,
    note = null,
    icon,
    mono = false,
    disabled = false,
    inputmode,
    width,
    onenter,
    onkeydown,
    onblur,
    trail,
  }: {
    value?: string;
    input?: HTMLInputElement;
    label: string;
    /** The label is still read by screen readers. */
    hideLabel?: boolean;
    placeholder?: string;
    error?: string | null;
    /** A quiet line under the field: how the value was understood, a hint. */
    note?: string | null;
    icon?: IconName;
    mono?: boolean;
    disabled?: boolean;
    inputmode?: 'text' | 'numeric' | 'url';
    width?: string;
    onenter?: () => void;
    onkeydown?: (event: KeyboardEvent) => void;
    onblur?: () => void;
    trail?: Snippet;
  } = $props();

  const id = `field-${++seq}`;

  function keydown(event: KeyboardEvent): void {
    onkeydown?.(event);
    if (event.defaultPrevented) return;
    if (event.key === 'Enter' && onenter) {
      event.preventDefault();
      onenter();
    }
  }
</script>

<div class="field" style:width>
  <label for={id} class="label" class:sr-only={hideLabel}>{label}</label>
  <div class="box" class:bad={!!error} class:off={disabled}>
    {#if icon}<span class="ico"><Icon name={icon} size={15} /></span>{/if}
    <input
      {id}
      bind:this={input}
      bind:value
      class:mono
      {placeholder}
      {disabled}
      {inputmode}
      spellcheck="false"
      autocomplete="off"
      aria-invalid={error ? true : undefined}
      aria-describedby={error || note ? `${id}-msg` : undefined}
      onkeydown={keydown}
      {onblur}
    />
    {#if trail}{@render trail()}{/if}
  </div>
  {#if error}
    <p class="msg bad" id="{id}-msg" role="alert"><Icon name="alert" size={13} />{error}</p>
  {:else if note}
    <p class="msg" id="{id}-msg" aria-live="polite">{note}</p>
  {/if}
</div>

<style>
  .field {
    display: grid;
    gap: 5px;
    min-width: 0;
  }

  .label {
    color: var(--muted);
    font-size: var(--fs-cap);
  }

  .box {
    display: flex;
    align-items: center;
    gap: 6px;
    min-height: 31px;
    padding: 0 4px 0 9px;
    border: 1px solid var(--line);
    border-radius: var(--r-ctl);
    background: var(--surface-2);
    transition: border-color var(--fast);
  }

  .box:hover:not(.off),
  .box:focus-within {
    border-color: var(--muted);
  }

  /* The field itself carries the focus ring: the input inside has none. */
  .box:has(input:focus-visible) {
    outline: 2px solid var(--text);
    outline-offset: 1px;
  }

  .box.bad {
    border-color: color-mix(in srgb, var(--bad) 70%, var(--line));
  }

  .ico {
    display: flex;
    color: var(--muted);
  }

  input {
    flex: 1;
    min-width: 0;
    height: 29px;
    padding: 0;
    border: 0;
    background: transparent;
    outline: none;
  }

  input::placeholder {
    color: var(--muted);
    opacity: 0.8;
  }

  .msg {
    display: flex;
    align-items: flex-start;
    gap: 5px;
    color: var(--muted);
    font-size: var(--fs-cap);
    line-height: 1.35;
  }

  .msg.bad {
    color: var(--bad);
  }

  .msg :global(svg) {
    margin-top: 1px;
  }
</style>
