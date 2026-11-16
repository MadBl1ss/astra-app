<script lang="ts">
  import Icon, { type IconName } from './Icon.svelte';

  // A square button with only an icon. The label is its accessible name and
  // its tooltip: an icon alone never has to be guessed.
  let {
    icon,
    label,
    pressed,
    fill = false,
    disabled = false,
    size = 16,
    tone,
    tabindex,
    onclick,
    onkeydown,
    element = $bindable(),
  }: {
    icon: IconName;
    label: string;
    /** A toggle (a star): announced as pressed or not. */
    pressed?: boolean;
    fill?: boolean;
    disabled?: boolean;
    size?: number;
    tone?: 'warn' | 'bad';
    tabindex?: number;
    onclick?: (event: MouseEvent) => void;
    onkeydown?: (event: KeyboardEvent) => void;
    element?: HTMLButtonElement;
  } = $props();
</script>

<button
  bind:this={element}
  type="button"
  class="ib {tone ?? ''}"
  class:on={pressed}
  aria-label={label}
  title={label}
  aria-pressed={pressed}
  {disabled}
  {tabindex}
  {onclick}
  {onkeydown}
>
  <Icon name={icon} {size} {fill} />
</button>

<style>
  .ib {
    display: grid;
    flex: none;
    place-items: center;
    width: 28px;
    height: 28px;
    padding: 0;
    border: 0;
    border-radius: var(--r-ctl);
    background: transparent;
    color: var(--muted);
    transition:
      background var(--fast),
      color var(--fast);
  }

  .ib:hover:not(:disabled) {
    background: var(--surface-2);
    color: var(--text);
  }

  .ib.on {
    color: var(--text);
  }

  .ib.warn {
    color: var(--warn);
  }

  .ib.bad:hover:not(:disabled) {
    color: var(--bad);
  }

  .ib:disabled {
    opacity: 0.45;
  }
</style>
