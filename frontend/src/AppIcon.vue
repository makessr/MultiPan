<script setup lang="ts">
defineProps<{name:string}>()
const paths:Record<string,string>={
 copy:'M8 8h13v13H8z M3 16V3h13',
 files:'M3 7h7l2 2h9v11H3z M3 7V4h7l2 3',
 upload:'M12 16V3m-5 5 5-5 5 5 M4 15v5h16v-5',
 download:'M12 3v13m-5-5 5 5 5-5 M4 17v4h16v-4',
 tasks:'M8 6h12M8 12h12M8 18h12 M3 6h.01M3 12h.01M3 18h.01',
 plus:'M12 5v14M5 12h14',
 refresh:'M20 8a8 8 0 1 0 0 8 M20 3v5h-5',
 back:'m15 5-7 7 7 7',
 forward:'m9 5 7 7-7 7',
 up:'M12 20V4M5 11l7-7 7 7',
 search:'M20 20l-5-5 M17 10a7 7 0 1 1-14 0 7 7 0 0 1 14 0',
 file:'M6 3h8l4 4v14H6z M14 3v5h4 M9 13h6M9 17h6',
 arrow:'m9 5 7 7-7 7',
 close:'m6 6 12 12M6 18 18 6',
 check:'m5 12 4 4L19 6',
 move:'M4 8h16m-4-4 4 4-4 4 M20 16H4m4-4-4 4 4 4',
 edit:'m4 16 11-11 4 4L8 20H4z M13 7l4 4',
 share:'M9 12 16 6M9 12l7 6 M7 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6 M19 7a2 2 0 1 0 0-4 2 2 0 0 0 0 4 M19 21a2 2 0 1 0 0-4 2 2 0 0 0 0 4',
 settings:'M4 7h16M4 17h16 M8 4v6M16 14v6',
 trash:'M4 7h16 M9 7V4h6v3 M6 7l1 13h10l1-13 M10 11v6M14 11v6'
}
</script>
<template><svg class="app-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><template v-if="name==='more'"><circle v-for="x in [5,12,19]" :key="x" :cx="x" cy="12" r="1.8" fill="currentColor" stroke="none"/></template><path v-else :d="paths[name]??paths.file"/></svg></template>
