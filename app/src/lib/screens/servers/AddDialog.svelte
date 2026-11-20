<script lang="ts">
  import { tick } from 'svelte';
  import Button from '../../components/Button.svelte';
  import Dialog from '../../components/Dialog.svelte';
  import Field from '../../components/Field.svelte';
  import { ru } from '../../i18n/ru';
  import { daemon } from '../../store.svelte';
  import { linkKind } from '../../sublink';

  // One field takes either kind of link: a single server (vless://…) is
  // added as is, anything else is a subscription, downloaded right away —
  // https://… or a link a provider made for its app (happ://add/…), which
  // the daemon unwraps. What the daemon objects to is shown under the
  // field, in its own words.
  let { onclose, onadded }: { onclose: () => void; onadded: (id: string) => void } = $props();

  const t = ru.addDialog;
  let link = $state('');
  let name = $state('');
  let error = $state<string | null>(null);
  let busy = $state(false);
  let input: HTMLInputElement | undefined = $state();

  const isSub = $derived(linkKind(link) === 'sub');

  async function submit(): Promise<void> {
    const value = link.trim();
    if (!value) {
      error = t.empty;
      return;
    }
    error = null;
    busy = true;
    try {
      // Not a link at all goes to subs.add too: its refusal explains what a
      // subscription link looks like, apps' links included.
      if (linkKind(value) === 'server') {
        const res = await daemon.call('subs.addLink', { link: value });
        onadded(res.id);
      } else {
        const res = await daemon.call('subs.add', name.trim() ? { url: value, name: name.trim() } : { url: value });
        onadded(res.id);
      }
      onclose();
    } catch (err) {
      error = (err as Error).message;
    } finally {
      busy = false;
    }
    // The field was disabled while waiting and lost the focus; the fix is
    // typed there.
    if (error) void tick().then(() => input?.focus());
  }
</script>

<Dialog title={t.title} width={480} {onclose}>
  <Field
    label={t.link}
    bind:value={link}
    bind:input
    placeholder={t.placeholder}
    inputmode="url"
    mono
    {error}
    note={busy && isSub ? t.busySub : t.note}
    disabled={busy}
    onenter={submit}
  />
  {#if isSub}
    <Field label={t.name} bind:value={name} placeholder={t.namePlaceholder} disabled={busy} onenter={submit} />
  {/if}
  {#snippet actions()}
    <Button variant="ghost" onclick={onclose}>{t.cancel}</Button>
    <Button icon="plus" {busy} onclick={submit}>{t.submit}</Button>
  {/snippet}
</Dialog>
