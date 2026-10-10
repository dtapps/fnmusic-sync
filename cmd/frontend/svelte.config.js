import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

// 以下 a11y 告警源于本项目「标签 + 自定义控件」的布局模式（Input/Select/Switch 为自定义组件，
// label 无法用 for/id 关联到内部控件；折叠面板、对话框遮罩使用 div 承载点击），属设计层面的已知噪声，
// 在此统一过滤，避免污染构建输出。如需真正无障碍可用，应改为 <button>/role + 键盘事件。
const ignoredA11yWarnings = new Set([
  'a11y_label_has_associated_control',
  'a11y_click_events_have_key_events',
  'a11y_no_static_element_interactions',
]);

export default {
  // Svelte 5 runes 模式
  preprocess: vitePreprocess(),
  compilerOptions: {
    runes: true,
  },
  onwarn(warning, handler) {
    if (ignoredA11yWarnings.has(warning.code)) return;
    handler(warning);
  },
};
