import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import './style.css'
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'
import './theme.css'
import * as Icons from '@element-plus/icons-vue'
import App from './App.vue'

const app = createApp(App)
// 全局错误可见化:未捕获的组件错误直接弹在界面(不再只沉控制台,便于用户报障)
app.config.errorHandler = (err, instance, info) => {
  console.error('[Vue error]', info, err)
  try {
    const { ElMessage } = require('element-plus')
    ElMessage.error('界面错误:' + (err && err.message ? err.message : String(err)))
  } catch { /* element 未就绪 */ }
}
app.use(ElementPlus, { locale: zhCn })
for (const [name, comp] of Object.entries(Icons)) app.component(name, comp)
app.mount('#app')
