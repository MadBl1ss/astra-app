<script lang="ts">
  import ToastStack from '../components/ToastStack.svelte';
  import { toasts } from '../toasts.svelte';

  // In a browser tab there is no second window: the same stack is drawn
  // over the page, in the corner where the app's own window would stand.
</script>

{#if toasts.mode === 'layer'}
  <div class="layer">
    <ToastStack
      view={toasts.view}
      onaction={(id, action) => toasts.act(id, action)}
      ondismiss={(id) => toasts.dismiss(id)}
      onmore={() => toasts.more()}
      onhover={(on) => toasts.hover(on)}
    />
  </div>
{/if}

<style>
  .layer {
    position: fixed;
    z-index: 60;
    right: 12px;
    bottom: 12px;
  }
</style>
