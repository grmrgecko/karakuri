<script lang="ts">
  let { hours = 0, minutes = 0, seconds = 0, faceStyle = "dark", accentColor = "", sweep = false }: { hours: number; minutes: number; seconds: number; faceStyle: string; accentColor: string; sweep: boolean; } = $props();

  let colors = $derived((() => {
    if (faceStyle === "light") {
      return { face: "#eff1f5", marks: "#4c4f69", hands: "#4c4f69", border: "#4c4f69" };
    }
    if (faceStyle === "transparent") {
      return { face: "none", marks: "currentColor", hands: "currentColor", border: "currentColor" };
    }
    return { face: "#1e1e2e", marks: "#cdd6f4", hands: "#cdd6f4", border: "#cdd6f4" };
  })());

  let accent = $derived(accentColor || "#89b4fa");
  let hourAngle = $derived((hours / 12) * 360);
  let minuteAngle = $derived((minutes / 60) * 360);
  let secondAngle = $derived((seconds / 60) * 360);

  // Place numbers at positions that follow the square contour rather than a circle.
  // Each number is mapped to a coordinate that sits evenly inside the rounded rectangle.
  const numberPositions = [
    { num: 12, x: 100, y: 24 },
    { num: 1, x: 134, y: 28 },
    { num: 2, x: 162, y: 50 },
    { num: 3, x: 176, y: 100 },
    { num: 4, x: 162, y: 150 },
    { num: 5, x: 134, y: 172 },
    { num: 6, x: 100, y: 176 },
    { num: 7, x: 66, y: 172 },
    { num: 8, x: 38, y: 150 },
    { num: 9, x: 24, y: 100 },
    { num: 10, x: 38, y: 50 },
    { num: 11, x: 66, y: 28 },
  ];

  // Short tick marks at each hour position, radiating from center.
  let hourTicks = $derived(
    numberPositions.map((pos) => {
      const dx = pos.x - 100;
      const dy = pos.y - 100;
      const dist = Math.sqrt(dx * dx + dy * dy);
      const nx = dx / dist;
      const ny = dy / dist;
      // Place ticks just outside the number labels.
      const startDist = dist + 6;
      const endDist = dist + 12;
      return {
        x1: 100 + nx * startDist,
        y1: 100 + ny * startDist,
        x2: 100 + nx * endDist,
        y2: 100 + ny * endDist,
      };
    })
  );
</script>

<svg viewBox="0 0 200 200" xmlns="http://www.w3.org/2000/svg" class="clock-svg">
  <!-- Rounded rectangle face -->
  <rect x="5" y="5" width="190" height="190" rx="20" ry="20"
    fill={colors.face} stroke={colors.border} stroke-width="2"
  />

  <!-- Hour tick marks -->
  {#each hourTicks as tick}
    <line
      x1={tick.x1} y1={tick.y1} x2={tick.x2} y2={tick.y2}
      stroke={colors.marks} stroke-width="1.5" stroke-linecap="round" opacity="0.5"
    />
  {/each}

  <!-- Number labels -->
  {#each numberPositions as pos}
    <text
      x={pos.x} y={pos.y}
      text-anchor="middle" dominant-baseline="central"
      fill={colors.marks} font-size="13" font-family="inherit" font-weight="500"
    >{pos.num}</text>
  {/each}

  <!-- Hour hand -->
  <line
    x1="100" y1="100" x2="100" y2="50"
    stroke={colors.hands} stroke-width="4" stroke-linecap="round"
    transform="rotate({hourAngle}, 100, 100)"
  />

  <!-- Minute hand -->
  <line
    x1="100" y1="100" x2="100" y2="28"
    stroke={colors.hands} stroke-width="2.5" stroke-linecap="round"
    transform="rotate({minuteAngle}, 100, 100)"
  />

  <!-- Second hand -->
  <line
    x1="100" y1="118" x2="100" y2="22"
    stroke={accent} stroke-width="1" stroke-linecap="round"
    transform="rotate({secondAngle}, 100, 100)"
  />

  <!-- Center cap -->
  <circle cx="100" cy="100" r="3.5" fill={accent} />
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
