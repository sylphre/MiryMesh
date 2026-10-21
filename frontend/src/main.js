// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router/index.js'
import './assets/css/tokens.css'
import './assets/css/base.css'
import './assets/css/layout.css'
import './assets/css/components.css'

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')
