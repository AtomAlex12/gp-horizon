<script lang="ts">
  // A screen the design has but the API doesn't feed yet — said plainly,
  // instead of showing made-up data.
  let { title, what }: { title: string; what: string } = $props();
</script>

<section class="card empty">
  <h2>{title}</h2>
  <p>{what}</p>
  <p class="hint">Появится в следующих бетах — в nuxk-core для этого пока нет данных.</p>
</section>

<style>
  h2 {
    font-size: 15px;
    color: var(--ink);
    margin-bottom: 8px;
  }
  p {
    max-width: 520px;
    margin: 0 auto 6px;
  }
</style>
