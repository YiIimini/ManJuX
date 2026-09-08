<!-- 小说目录树:任意目录层级递归展示(卷/册/章…),目录节点可折叠 -->
<script setup>
const props = defineProps({
  nodes: { type: Array, default: () => [] },
  expanded: { type: Set, default: () => new Set() },
  reading: { type: Object, default: null },
  depth: { type: Number, default: 0 },
})
const emit = defineEmits(['read', 'toggle'])
</script>

<template>
  <template v-for="node in nodes" :key="node.key || (node.ch && node.ch.path)">
    <!-- 目录节点:折叠/展开,任意层级缩进 -->
    <div v-if="node.type === 'dir'" class="tree-dir"
         :style="{ paddingLeft: (depth * 16 + 10) + 'px' }"
         @click="emit('toggle', node.key)">
      <el-icon :size="11" color="var(--mx-amber)"
               :style="{ transform: expanded.has(node.key) ? 'rotate(90deg)' : '', flex: 'none' }"><CaretRight /></el-icon>
      <span class="truncate">{{ node.name }}</span>
      <span class="timecode" style="font-size:9px;margin-left:auto">{{ node.children.length }}</span>
    </div>
    <ChapterTree v-if="node.type === 'dir' && expanded.has(node.key)"
                 :nodes="node.children" :expanded="expanded" :reading="reading" :depth="depth + 1"
                 @read="emit('read', $event)" @toggle="emit('toggle', $event)" />
    <!-- 章节文件(叶子) -->
    <div v-else-if="node.type === 'file'" class="tree-leaf"
         :class="{ on: reading && reading.name === node.ch.name }"
         :style="{ paddingLeft: (depth * 16 + 24) + 'px' }"
         @click="emit('read', node.ch)">
      <el-icon :size="11" color="var(--mx-text3)" style="flex:none"><Document /></el-icon>
      <span class="truncate">{{ node.name }}</span>
      <span class="timecode" style="font-size:9px">{{ node.ch.kb }}KB</span>
    </div>
  </template>
</template>
