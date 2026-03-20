<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'

const port = window.location.port || '—'
const clock = ref(new Date().toLocaleTimeString())
const counter = ref(0)
const headingColor = ref('#fdba74')

let timer: ReturnType<typeof setInterval>

onMounted(() => {
  timer = setInterval(() => {
    clock.value = new Date().toLocaleTimeString()
  }, 1000)
})

onUnmounted(() => {
  clearInterval(timer)
})
</script>

<template>
  <div class="page">
    <h1 :style="{ color: headingColor }">Vue 3 + TypeScript</h1>

    <p class="meta">
      Port <strong class="port">{{ port }}</strong>
      &middot;
      {{ clock }}
    </p>

    <!-- Counter -->
    <div class="card">
      <h2>Counter</h2>
      <div class="counter-row">
        <button class="counter-btn" @click="counter--">&minus;</button>
        <span class="counter-val">{{ counter }}</span>
        <button class="counter-btn" @click="counter++">+</button>
      </div>
    </div>

    <!-- Color Picker -->
    <div class="card">
      <h2>Heading Color</h2>
      <p class="hint">Pick a color to change the heading above</p>
      <div class="color-row">
        <input type="color" v-model="headingColor" class="color-input" />
        <span class="color-hex">{{ headingColor }}</span>
      </div>
    </div>

    <p class="footer">
      If mdp is working, you should see a floating switcher widget at the top of this page.
    </p>
  </div>
</template>

<style scoped>
* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}

.page {
  min-height: 100vh;
  background: linear-gradient(135deg, #3d1c00 0%, #b45309 100%);
  color: #fed7aa;
  font-family: 'SF Mono', 'Fira Code', 'Cascadia Code', monospace;
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 60px 20px 40px;
}

h1 {
  font-size: 2.4rem;
  font-weight: 700;
  margin-bottom: 8px;
  letter-spacing: -0.5px;
  transition: color 0.3s ease;
}

.meta {
  margin-bottom: 32px;
  opacity: 0.6;
  font-size: 0.9rem;
}

.port {
  color: #f59e0b;
}

.card {
  background: rgba(0, 0, 0, 0.25);
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 12px;
  padding: 24px 32px;
  margin-bottom: 24px;
  text-align: center;
  min-width: 300px;
}

.card h2 {
  margin-bottom: 16px;
  font-size: 1.1rem;
  color: #fbbf24;
}

.counter-row {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 16px;
}

.counter-btn {
  width: 40px;
  height: 40px;
  border-radius: 8px;
  border: 1px solid rgba(255, 255, 255, 0.15);
  background: rgba(255, 255, 255, 0.06);
  color: #fed7aa;
  font-size: 1.4rem;
  cursor: pointer;
  transition: background 0.15s;
}

.counter-btn:hover {
  background: rgba(255, 255, 255, 0.12);
}

.counter-val {
  font-size: 2rem;
  font-weight: 700;
  min-width: 60px;
  color: #f59e0b;
}

.hint {
  font-size: 0.8rem;
  opacity: 0.4;
  margin-bottom: 12px;
}

.color-row {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
}

.color-input {
  width: 48px;
  height: 48px;
  border: 2px solid rgba(255, 255, 255, 0.15);
  border-radius: 8px;
  background: transparent;
  cursor: pointer;
  padding: 2px;
}

.color-hex {
  font-size: 1rem;
  color: #fbbf24;
  font-weight: 600;
}

.footer {
  margin-top: auto;
  padding-top: 40px;
  font-size: 0.8rem;
  opacity: 0.4;
  text-align: center;
  max-width: 400px;
  line-height: 1.5;
}
</style>
