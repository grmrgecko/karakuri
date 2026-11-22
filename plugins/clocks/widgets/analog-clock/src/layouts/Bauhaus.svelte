<script lang="ts">
  let { hours = 0, minutes = 0, faceStyle = "dark", accentColor = "" }: { hours: number; minutes: number; faceStyle: string; accentColor: string; } = $props();

  let colors = $derived((() => {
    if (faceStyle === "light") {
      return { face: "#eff1f5", stroke: "#4c4f69", hands: "#4c4f69" };
    }
    if (faceStyle === "transparent") {
      return { face: "none", stroke: "currentColor", hands: "currentColor" };
    }
    return { face: "#1e1e2e", stroke: "#cdd6f4", hands: "#cdd6f4" };
  })());

  let hourAngle = $derived((hours / 12) * 360);
  let minuteAngle = $derived((minutes / 60) * 360);
</script>

<svg viewBox="0 0 200 200" xmlns="http://www.w3.org/2000/svg" class="clock-svg">
  <!-- Simple circle outline — no numbers, no tick marks -->
  <circle cx="100" cy="100" r="92" fill={colors.face} stroke={colors.stroke} stroke-width="2" />

  <!-- Hour hand — thin geometric rectangle -->
  <rect
    x="-2" y="-44" width="4" height="46" rx="1"
    fill={colors.hands}
    transform="translate(100, 100) rotate({hourAngle})"
  />

  <!-- Minute hand — thinner and longer -->
  <rect
    x="-1.5" y="-68" width="3" height="70" rx="0.75"
    fill={colors.hands}
    transform="translate(100, 100) rotate({minuteAngle})"
  />

  <!-- Small center dot -->
  <circle cx="100" cy="100" r="2.5" fill={colors.hands} />
</svg>

<style>
  .clock-svg {
    width: 100%;
    height: 100%;
    max-width: 100%;
    max-height: 100%;
    display: block;
  }
</style>
