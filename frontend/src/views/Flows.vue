<template>
  <div class="page-card">
    <el-tabs v-model="activeTab" class="module-tabs" @tab-change="onTabChange">
      <el-tab-pane label="采集流程" name="flows" />
      <el-tab-pane label="脚本管理" name="scripts" />
      <el-tab-pane label="任务监控" name="tasks" />
    </el-tabs>

    <div v-show="activeTab === 'flows'">
      <div class="toolbar">
      <el-select v-model="filterStatus" clearable placeholder="状态" style="width: 120px" @change="reload">
        <el-option label="已创建" value="created" />
        <el-option label="生效中" value="active" />
        <el-option label="已停用" value="disabled" />
        <el-option label="已归档" value="archived" />
      </el-select>
      <el-select v-model="filterType" clearable placeholder="采集方式" style="width: 160px" @change="reload">
        <el-option label="SQL" value="sql" />
        <el-option label="日志解析" value="logparse" />
        <el-option label="Python 采集(沙箱)" value="python" />
        <el-option label="脚本采集(目标机)" value="script" />
      </el-select>
      <el-input v-model="filterMetric" clearable placeholder="指标编码" style="width: 160px" @keyup.enter="reload" />
      <el-button type="primary" :icon="Search" @click="reload">查询</el-button>
      <el-button type="success" :icon="Plus" @click="openCreate">新建流程</el-button>
      <el-button type="primary" plain :icon="MagicStick" @click="aiAssistantVisible = true">AI 助手</el-button>
      <div style="flex: 1" />
      <el-button :icon="Refresh" circle @click="reload" />
    </div>

    <el-table :data="items" v-loading="loading" border stripe>
      <el-table-column prop="id" label="ID" width="60" />
      <el-table-column prop="flowCode" label="流程编码" min-width="140" show-overflow-tooltip />
      <el-table-column prop="name" label="名称" min-width="160" show-overflow-tooltip />
      <el-table-column prop="metricCode" label="指标" min-width="130" show-overflow-tooltip />
      <el-table-column label="采集方式" width="140">
        <template #default="{ row }">
          <el-tag size="small" :type="typeTag(row.collectType)">{{ typeLabel(row.collectType) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="调度" min-width="150" show-overflow-tooltip>
        <template #default="{ row }">
          {{ row.scheduleMode === "trading_day" ? `交易日 ${row.windowTime || "-"} (${bizDateLabel(row.bizDateMode)})` : row.cronExpr || "-" }}
        </template>
      </el-table-column>
      <el-table-column prop="version" label="版本" width="70" />
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag size="small" :type="statusTag(row.status)">{{ statusLabel(row.status) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="290" fixed="right">
        <template #default="{ row }">
          <el-button v-if="row.status !== 'archived'" link type="primary" :loading="previewingId === row.id" @click="doPreview(row)">预览</el-button>
          <el-button v-if="row.status === 'created' || row.status === 'disabled'" link type="success" @click="doLifecycle(row, 'activate', '生效')">生效</el-button>
          <el-button v-if="row.status === 'active'" link type="warning" :loading="triggeringId === row.id" @click="doTrigger(row)">手工触发</el-button>
          <el-button v-if="row.status === 'active'" link type="danger" @click="doLifecycle(row, 'disable', '停用')">停用</el-button>
          <el-button v-if="row.status !== 'archived'" link type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button v-if="row.status === 'created' || row.status === 'active' || row.status === 'disabled'" link type="info" @click="doLifecycle(row, 'archive', '归档')">归档</el-button>
          <el-button v-if="row.status === 'archived'" link type="danger" @click="doDelete(row)">删除</el-button>
          <el-button link type="info" @click="openDetail(row)">详情</el-button>
        </template>
      </el-table-column>
    </el-table>
    <el-empty v-if="!loading && error" :description="error" />
    <el-empty v-else-if="!loading && items.length === 0" description="暂无采集流程" />
    <el-pagination
      v-model:current-page="page"
      :page-size="pageSize"
      :total="total"
      layout="total, prev, pager, next"
      class="pager"
      @current-change="reload"
    />

    <!-- 新建/编辑 -->
    <el-dialog v-model="formVisible" :title="editingId ? '编辑采集流程' : '新建采集流程'" width="720px" top="5vh">
      <el-form :model="form" label-width="110px">
        <el-divider content-position="left">AI 生成配置</el-divider>
        <el-collapse v-model="aiActive" class="ai-collapse">
          <el-collapse-item title="用自然语言生成流程配置建议（FR-AI-01，生成后请人工审核）" name="ai">
            <el-form-item label="流程描述">
              <el-input v-model="aiDesc" type="textarea" :rows="3"
                placeholder="如：每个交易日 17:00 采集核心数据库表空间使用率（数据源 CORE_DB01），取最大值，单位 GB" />
            </el-form-item>
            <el-form-item label="系统编码">
              <el-select v-model="aiSystemCode" clearable filterable placeholder="可选，帮助 AI 生成更贴合的建议" style="width: 300px">
                <el-option v-for="s in systems" :key="s.systemCode" :label="s.name + ' (' + s.systemCode + ')'" :value="s.systemCode" />
              </el-select>
            </el-form-item>
            <el-form-item>
              <el-button type="primary" plain :icon="MagicStick" :loading="aiGenLoading" @click="doAiGen">生成配置建议</el-button>
              <el-button :disabled="!aiDraft" @click="applyAiDraft">应用到表单</el-button>
            </el-form-item>
            <el-alert v-if="aiNotice" type="warning" :closable="false" :title="aiNotice" style="margin-bottom: 8px" />
            <pre v-if="aiDraftRaw" class="json-pre ai-draft">{{ aiDraftRaw }}</pre>
          </el-collapse-item>
        </el-collapse>

        <el-divider content-position="left">基础信息</el-divider>
        <el-row :gutter="12">
          <el-col :span="12">
            <el-form-item label="流程编码" required>
              <el-input v-model="form.flowCode" :disabled="!!editingId" placeholder="唯一编码, 如 CORE01_DB_SIZE" />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="名称" required>
              <el-input v-model="form.name" />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="目标指标" required>
              <el-select v-model="form.metricCode" filterable :disabled="!!editingId" style="width: 100%" placeholder="选择活跃指标">
                <el-option v-for="m in metrics" :key="m.metricCode" :label="`${m.metricCode} (${m.name})`" :value="m.metricCode" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="数据源" :required="dsRequired">
              <el-select v-model="form.dataSourceId" filterable clearable style="width: 100%"
                :placeholder="form.collectType === 'python' ? '可选: Python 采集不使用数据源' : '选择数据源(与分类二选一)'"
                @change="form.dataSourceCategoryId = null">
                <el-option v-for="d in selectableDatasources" :key="d.id" :label="d.name" :value="d.id" :disabled="d.status === 'disabled'" />
              </el-select>
              <div v-if="form.collectType === 'python'" class="field-hint">平台沙箱不访问数据源, 此处可留空</div>
              <div v-if="form.collectType === 'script'" class="field-hint">脚本采集须选择"执行目标"(类型 ssh)数据源; 脚本与参数下发到该主机的临时目录执行</div>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="数据源分类">
              <el-select v-if="form.collectType === 'logparse'" v-model="form.dataSourceCategoryId" filterable clearable style="width: 100%" placeholder="可选: 分类下日志数据源共用此配置" @change="form.dataSourceId = null">
                <el-option v-for="c in dsCategories" :key="c.id" :label="`${c.name} (${c.systemCode})`" :value="c.id" />
              </el-select>
              <el-input v-else disabled placeholder="仅日志解析可选数据源分类" />
            </el-form-item>
          </el-col>
          <el-col :span="24">
            <el-form-item label="采集方式" required>
              <el-radio-group v-model="form.collectType" :disabled="!!editingId">
                <el-radio value="sql">SQL</el-radio>
                <el-radio value="logparse">日志解析</el-radio>
                <el-radio value="python">Python 采集（平台沙箱·不访问目标机）</el-radio>
                <el-radio value="script">脚本采集（目标机执行）</el-radio>
              </el-radio-group>
              <div v-if="form.collectType === 'python'" class="field-hint">
                脚本在平台侧沙箱内运行: 无网络、模块白名单、只读文件系统, 不连接任何目标机, 仅支持 Python, 无需选择数据源
              </div>
              <div v-else-if="form.collectType === 'script'" class="field-hint">
                脚本与参数下发到所选执行目标(ssh)的临时目录, 由目标机解释器运行, 可读取该主机本地数据, 支持 Python/Bash/PowerShell
              </div>
            </el-form-item>
          </el-col>
        </el-row>
        <el-alert v-if="form.collectType === 'logparse' && form.dataSourceCategoryId" type="info" :closable="false" style="margin-bottom: 12px">
          已选数据源分类: 执行时读取该分类下全部未停用日志数据源, 共用同一采集/清洗逻辑; 单个数据源读取失败不影响其他数据源
        </el-alert>

        <el-divider content-position="left">采集配置</el-divider>
        <el-form-item label="数据日期" required>
          <el-radio-group v-model="form.bizDateMode">
            <el-radio value="natural">当日</el-radio>
            <el-radio value="prev_day">上日</el-radio>
            <el-radio value="calendar">上一交易日</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-alert v-if="form.bizDateMode !== 'natural'" type="info" :closable="false" style="margin-bottom: 12px">
          数据日期决定采集模板 ${biz_date} 变量取值与数据落地业务日; "上日"=执行日前一天自然日, "上一交易日"按交易日日历推导(执行日前的最近一个交易日)
        </el-alert>
        <template v-if="form.collectType === 'sql'">
          <el-form-item label="SQL 模板" required>
            <el-input v-model="form.sqlConfig.sqlTemplate" type="textarea" :rows="5" placeholder="SELECT ... WHERE biz_date = '${biz_date}'" />
          </el-form-item>
          <el-alert type="info" :closable="false" style="margin-bottom: 12px">
            支持变量: ${biz_date} ${prev_trading_day} ${next_trading_day} ${system_code} ${hour} ${trading_day_offset:N} ${trading_day_offset:N:格式}; 日期变量可自定义格式, 如 ${prev_trading_day:YYYYMMDD}
          </el-alert>
          <el-row :gutter="12">
            <el-col :span="12">
              <el-form-item label="取值列">
                <el-input v-model="form.sqlConfig.valueColumn" placeholder="结果集中取值列名(留空取首列)" />
              </el-form-item>
            </el-col>
            <el-col :span="12">
              <el-form-item label="多行聚合">
                <el-select v-model="form.sqlConfig.aggregate" style="width: 100%">
                  <el-option label="直取(单值)" value="single" />
                  <el-option label="求和 sum" value="sum" />
                  <el-option label="平均 avg" value="avg" />
                  <el-option label="最大 max" value="max" />
                  <el-option label="最小 min" value="min" />
                </el-select>
              </el-form-item>
            </el-col>
          </el-row>
        </template>
        <template v-else-if="form.collectType === 'logparse'">
          <el-row :gutter="12">
            <el-col :span="12">
              <el-form-item label="解析模式" required>
                <el-select v-model="form.logConfig.mode" style="width: 100%">
                  <el-option label="正则表达式" value="regex" />
                  <el-option label="分隔符(CSV)" value="delimited" />
                  <el-option label="JSON 字段" value="json" />
                  <el-option label="段落聚合" value="section" />
                </el-select>
              </el-form-item>
            </el-col>
            <el-col :span="12">
              <el-form-item label="文件 glob">
                <el-input v-model="form.logConfig.fileGlob" placeholder="留空用数据源默认; 支持 ${prev_trading_day:YYYYMMDD} 等变量" />
                <div class="glob-hint">
                  <span v-if="form.logConfig.fileGlob">实际生效(流程优先): <code>{{ form.logConfig.fileGlob }}</code></span>
                  <span v-else-if="catLogSources.length > 0">
                    流程未填, 实际生效(数据源 globTemplate){{ catLogSources.length > 1 ? ` × ${catLogSources.length} 台服务器` : "" }}:
                    <code v-for="g in catLogGlobs" :key="g">{{ g }}</code>
                  </span>
                  <span v-else>留空时按“数据源 globTemplate → pathTemplate”取实际匹配模式</span>
                </div>
              </el-form-item>
            </el-col>
          </el-row>
          <el-form-item v-if="form.logConfig.mode === 'regex'" label="正则表达式" required>
            <el-input v-model="form.logConfig.pattern" placeholder="如 (?<value>\d+(?:\.\d+)?)\s*(?<unit>\w+)" />
          </el-form-item>
          <template v-else-if="form.logConfig.mode === 'delimited'">
            <el-row :gutter="12">
              <el-col :span="8">
                <el-form-item label="分隔符" required>
                  <el-input v-model="form.logConfig.delimiters" placeholder="如 , 或 |" />
                </el-form-item>
              </el-col>
              <el-col :span="8">
                <el-form-item label="字段下标">
                  <el-input v-model="fieldsText" placeholder='JSON: {"value":2,"unit":3}' />
                </el-form-item>
              </el-col>
            </el-row>
          </template>
          <el-row :gutter="12" v-if="form.logConfig.mode !== 'section'">
            <el-col :span="12">
              <el-form-item label="取值字段" required>
                <el-input v-model="form.logConfig.valueField" placeholder="命名捕获组/字段名/列名" />
              </el-form-item>
            </el-col>
            <el-col :span="12">
              <el-form-item label="单位字段">
                <el-input v-model="form.logConfig.unitField" placeholder="可选, 用于单位换算" />
              </el-form-item>
            </el-col>
          </el-row>
        </template>
        <template v-if="form.collectType === 'logparse' && form.logConfig.mode === 'section'">
          <el-row :gutter="12">
            <el-col :span="12">
              <el-form-item label="起始标记" required>
                <el-input v-model="form.logConfig.startMarker" placeholder="行内含该文本视为段落开始, 如: 功能号,次数,总用时,最快,最慢,平均,名称" />
              </el-form-item>
            </el-col>
            <el-col :span="12">
              <el-form-item label="结束标记">
                <el-input v-model="form.logConfig.endMarker" placeholder="行内含该文本视为段落结束, 如: 个服务被调用" />
              </el-form-item>
            </el-col>
          </el-row>
          <el-form-item label="行正则" required>
            <el-input v-model="form.logConfig.linePattern" type="textarea" :rows="2"
              placeholder="命名分组正则, 须含 func/count 分组, 如: ^\\S+ \\S+ (?P<func>\\S+)\\s+(?P<count>\\d+),\\s*(?P<total>\\d+(?:\\.\\d+)?),\\s*(?P<fastest>\\d+(?:\\.\\d+)?),\\s*(?P<slowest>\\d+(?:\\.\\d+)?),\\s*(?P<avg>\\d+(?:\\.\\d+)?)" />
          </el-form-item>
          <el-row :gutter="12">
            <el-col :span="12">
              <el-form-item label="聚合前缀">
                <el-input v-model="form.logConfig.prefixes" placeholder="逗号分隔, 仅统计这些前缀, 如: DT01,DT02; 留空=全部" />
              </el-form-item>
            </el-col>
            <el-col :span="12">
              <el-form-item label="排除前缀">
                <el-input v-model="form.logConfig.excludePrefixes" placeholder="逗号分隔, 用于其他类, 如: DT01,DT02,GT01,GT02,JT01,JT02" />
              </el-form-item>
            </el-col>
          </el-row>
          <el-row :gutter="12">
            <el-col :span="12">
              <el-form-item label="输出值" required>
                <el-select v-model="form.logConfig.output" style="width: 100%">
                  <el-option label="调用次数 count" value="count" />
                  <el-option label="总用时 total" value="total" />
                  <el-option label="最快耗时 fastest" value="fastest" />
                  <el-option label="最慢耗时 slowest" value="slowest" />
                  <el-option label="平均耗时 avg(按次数加权)" value="avg" />
                </el-select>
              </el-form-item>
            </el-col>
          </el-row>
          <el-alert type="info" :closable="false" style="margin-bottom: 12px">
            段落聚合读取目录下全部匹配文件(编码按数据源 encoding 配置, 如 gbk); 按聚合前缀过滤后汇总: count=Σ次数, fastest=最小, slowest=最大, avg=Σ总用时/Σ次数(调用次数加权平均); 聚合分组建议用 \\S+(形态可能是 S01703/310005/DT01SHZ1001)
          </el-alert>
          <el-alert v-if="form.dataSourceCategoryId" type="warning" :closable="false" style="margin-bottom: 12px">
            已选数据源分类: 分类下每台服务器的日志使用同一段落处理逻辑, 再跨服务器合并(次数求和 / 最快取最小 / 最慢取最大 / 平均按调用次数加权); 单台服务器无匹配文件或读取失败不影响其他服务器
          </el-alert>
        </template>
        <template v-else-if="form.collectType === 'python'">
          <el-row :gutter="12">
            <el-col :span="12">
              <el-form-item label="脚本" required>
                <el-select v-model="form.pyConfig.scriptId" clearable filterable style="width: 100%" placeholder="选择已登记脚本">
                  <el-option v-for="s in pythonCollectScripts" :key="s.id" :label="`${s.name} (${s.scriptType})`" :value="s.id" />
                </el-select>
                <div class="field-hint">平台沙箱仅支持 Python 脚本; Bash/PowerShell 请改用"脚本采集(目标机执行)"</div>
              </el-form-item>
            </el-col>
            <el-col :span="12">
              <el-form-item label="脚本版本">
                <el-input-number v-model="form.pyConfig.version" :min="0" style="width: 100%" placeholder="留空/0 = 生效版本" />
              </el-form-item>
            </el-col>
          </el-row>
        </template>

        <template v-else>
          <el-alert type="warning" :closable="false" style="margin-bottom: 12px"
            title="脚本采集在执行目标主机上真实执行: 脚本与参数经 SFTP 落到临时目录(执行后删除); 出参为 stdout 最后一行 JSON" />
          <el-row :gutter="12">
            <el-col :span="12">
              <el-form-item label="脚本" required>
                <el-select v-model="form.scriptConfig.scriptId" clearable filterable style="width: 100%" placeholder="选择已登记脚本">
                  <el-option v-for="s in collectScripts" :key="s.id"
                    :label="`${s.name} (${s.scriptType}/${languageLabel(s.language)})`" :value="s.id" />
                </el-select>
              </el-form-item>
            </el-col>
            <el-col :span="12">
              <el-form-item label="脚本版本">
                <el-input-number v-model="form.scriptConfig.version" :min="0" style="width: 100%" placeholder="留空/0 = 生效版本" />
              </el-form-item>
            </el-col>
          </el-row>
          <el-form-item label="输入参数">
            <div class="param-list">
              <div v-for="(row, i) in form.scriptConfig.params" :key="i" class="param-row">
                <el-input v-model="row.key" placeholder="参数名, 如 mount" style="width: 220px" />
                <el-input v-model="row.value" placeholder="参数值, 如 /data" style="width: 320px" />
                <el-button link type="danger" @click="form.scriptConfig.params.splice(i, 1)">删除</el-button>
              </div>
              <el-button link type="primary" @click="form.scriptConfig.params.push({ key: '', value: '' })">+ 添加参数</el-button>
            </div>
            <div class="field-hint">
              脚本从同目录 params.json 读取; 平台内置变量(biz_date/prev_trading_day/system_code 等)会自动合并下发且优先于同名自定义参数
            </div>
          </el-form-item>
        </template>

        <el-divider content-position="left">调度配置</el-divider>
        <el-form-item label="调度模式" required>
          <el-radio-group v-model="form.scheduleMode">
            <el-radio value="normal">普通 cron</el-radio>
            <el-radio value="trading_day">交易日模式</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="form.scheduleMode === 'normal'" label="cron 表达式" required>
          <el-input v-model="form.cronExpr" placeholder="5 段(分钟级) 或 6 段(秒级), 如 0 17 * * * 或 0 0 17 * * *" style="width: 300px" />
        </el-form-item>
        <el-form-item v-else label="非交易时点" required>
          <el-select v-model="form.windowTime" style="width: 240px" placeholder="选择时点">
            <el-option-group label="当日（收盘后 15:00 - 23:45）">
              <el-option v-for="t in windowOptionsEvening" :key="t" :label="t" :value="t" />
            </el-option-group>
            <el-option-group label="次日（开盘前 00:00 - 09:00）">
              <el-option v-for="t in windowOptionsMorning" :key="t" :label="t" :value="t" />
            </el-option-group>
          </el-select>
        </el-form-item>
        <div v-if="form.scheduleMode === 'trading_day'" style="margin: -8px 0 12px 110px; color: var(--el-text-color-secondary); font-size: 12px">
          仅支持非交易时点: 15:00 至次日 09:00, 15 分钟一个间隔; 交易日该时点触发采集
        </div>

        <el-divider content-position="left">清洗配置</el-divider>
        <el-row :gutter="12">
          <el-col :span="12">
            <el-form-item label="多值取值">
              <el-select v-model="form.clean.dedupTake" clearable placeholder="默认按指标统计方式" style="width: 100%">
                <el-option label="末次 latest" value="latest" />
                <el-option label="首次 first" value="first" />
                <el-option label="平均 avg" value="avg" />
                <el-option label="最大 max" value="max" />
                <el-option label="最小 min" value="min" />
                <el-option label="求和 sum" value="sum" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="脚本清洗">
              <el-select v-model="form.clean.scriptId" clearable filterable placeholder="可选: Python 清洗脚本" style="width: 100%">
                <el-option v-for="s in scripts" :key="s.id" :label="s.name" :value="s.id" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>
        <el-form-item label="转换规则">
          <div v-for="(tr, i) in form.clean.transforms" :key="i" class="transform-row">
            <el-select v-model="tr.type" style="width: 110px">
              <el-option label="单位换算" value="unit" />
              <el-option label="线性变换" value="linear" />
              <el-option label="保留小数" value="round" />
            </el-select>
            <el-input v-if="tr.type === 'unit'" v-model="tr.target" placeholder="目标单位, 如 GB" style="width: 140px" />
            <template v-else-if="tr.type === 'linear'">
              <el-input-number v-model="tr.a" :precision="4" controls-position="right" style="width: 130px" placeholder="a" />
              <span class="op">x +</span>
              <el-input-number v-model="tr.b" :precision="4" controls-position="right" style="width: 130px" placeholder="b" />
            </template>
            <el-input-number v-else v-model="tr.digits" :min="0" :max="8" style="width: 110px" />
            <el-button :icon="Delete" circle size="small" @click="form.clean.transforms.splice(i, 1)" />
          </div>
          <el-button link type="primary" :icon="Plus" @click="form.clean.transforms.push({ type: 'unit', target: '', a: 0, b: 0, digits: 2 })">添加转换规则</el-button>
        </el-form-item>

        <el-form-item label="备注">
          <el-input v-model="form.remark" type="textarea" :rows="2" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="formVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveForm">保存</el-button>
      </template>
    </el-dialog>

    <!-- 预览试跑结果 -->
    <el-dialog v-model="previewVisible" title="预览试跑结果（不落库/不判定）" width="720px" top="5vh">
      <el-descriptions v-if="preview" :column="3" border size="small">
        <el-descriptions-item label="状态">
          <el-tag :type="preview.status === 'success' ? 'success' : 'danger'">{{ preview.status }}</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="耗时">{{ preview.durationMs }} ms</el-descriptions-item>
        <el-descriptions-item label="数据日期">{{ preview.bizDate || "-" }}</el-descriptions-item>
        <el-descriptions-item label="采集行数">{{ preview.rowsCollected }}</el-descriptions-item>
        <el-descriptions-item label="解析失败率">{{ (preview.parseFailRate * 100).toFixed(1) }}%</el-descriptions-item>
        <el-descriptions-item label="异常值数">{{ preview.anomalyCount }}</el-descriptions-item>
        <el-descriptions-item label="清洗值">{{ preview.cleanValue ?? "-" }}</el-descriptions-item>
      </el-descriptions>
      <el-alert v-if="preview && preview.errorMsg" type="error" :title="preview.errorMsg" :closable="false" style="margin-top: 12px" />
      <template v-if="preview && preview.runLog">
        <el-divider content-position="left">执行日志（实际读取的文件 glob / 文件清单 / 数据源成败）</el-divider>
        <pre class="runlog-pre">{{ preview.runLog }}</pre>
      </template>
      <template v-if="preview && previewRaw.length">
        <el-divider content-position="left">原始数据（前 5 条）</el-divider>
        <el-table :data="previewRaw" size="small" border max-height="200">
          <el-table-column type="index" width="50" />
          <el-table-column prop="raw" label="原始值" show-overflow-tooltip />
        </el-table>
      </template>
    </el-dialog>

    <!-- 详情 -->
    <el-dialog v-model="detailVisible" title="流程详情" width="680px" top="5vh">
      <el-descriptions v-if="detail" :column="2" border size="small">
        <el-descriptions-item label="流程编码">{{ detail.flowCode }}</el-descriptions-item>
        <el-descriptions-item label="名称">{{ detail.name }}</el-descriptions-item>
        <el-descriptions-item label="指标">{{ detail.metricCode }}</el-descriptions-item>
        <el-descriptions-item label="数据源">{{ dsName(detail.dataSourceId) }}</el-descriptions-item>
        <el-descriptions-item label="数据源分类">{{ catName(detail.dataSourceCategoryId) }}</el-descriptions-item>
        <el-descriptions-item label="采集方式">{{ typeLabel(detail.collectType) }}</el-descriptions-item>
        <el-descriptions-item label="数据日期">{{ bizDateLabel(detail.bizDateMode) }}</el-descriptions-item>
        <el-descriptions-item label="调度">
          {{ detail.scheduleMode === "trading_day" ? `交易日 ${detail.windowTime || "-"} (${bizDateLabel(detail.bizDateMode)})` : detail.cronExpr }}
        </el-descriptions-item>
        <el-descriptions-item label="版本">{{ detail.version }}</el-descriptions-item>
        <el-descriptions-item label="状态">{{ statusLabel(detail.status) }}</el-descriptions-item>
        <el-descriptions-item label="创建人">{{ detail.createdBy || "-" }}</el-descriptions-item>
        <el-descriptions-item label="创建时间">{{ fmtTs(detail.createdAt) }}</el-descriptions-item>
        <el-descriptions-item label="备注" :span="2">{{ detail.remark || "-" }}</el-descriptions-item>
        <el-descriptions-item label="采集配置" :span="2">
          <pre class="json-pre">{{ prettyJson(detail.collectConfig) }}</pre>
        </el-descriptions-item>
        <el-descriptions-item label="清洗配置" :span="2">
          <pre class="json-pre">{{ prettyJson(detail.cleanConfig) }}</pre>
        </el-descriptions-item>
      </el-descriptions>
    </el-dialog>

    <AiAssistant v-model="aiAssistantVisible" />
    </div>

    <Scripts v-if="activeTab === 'scripts'" embedded />
    <Tasks v-if="activeTab === 'tasks'" embedded />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue"
import { ElMessage, ElMessageBox } from "element-plus"
import { Search, Refresh, Plus, Delete, MagicStick } from "@element-plus/icons-vue"
import { http } from "@/api/http"
import { fmtTs } from "@/utils/format"
import { useList } from "@/composables/useList"
import { useQueryTab } from "@/composables/useQueryTab"
import AiAssistant from "@/components/AiAssistant.vue"
import Scripts from "@/views/Scripts.vue"
import Tasks from "@/views/Tasks.vue"

interface FlowRow {
  id: number
  flowCode: string
  name: string
  metricCode: string
  dataSourceId: number | null
  dataSourceCategoryId: number | null
  collectType: string
  collectConfig: string
  cleanConfig: string
  scheduleMode: string
  cronExpr: string
  tradingMarket: string
  windowTime: string
  bizDateMode: string
  version: number
  status: string
  enabled: boolean
  remark: string
  createdBy: string
  createdAt: string
}
interface MetRow { metricCode: string; name: string; status: string }
interface DsRow { id: number; name: string; status: string; type?: string; categoryId?: number | null; config?: string }
interface DsCatRow { id: number; systemCode: string; name: string; remark?: string }
interface ScriptRow { id: number; name: string; scriptType: string; language?: string }
interface PreviewRes {
  ok: boolean; status: string; errorMsg: string; durationMs: number; bizDate?: string
  rowsCollected: number; parseFailRate: number; anomalyCount: number
  cleanValue: number | null; rawPreview: string[]
  runLog?: string; dsOk?: number[]; dsFailed?: number[]
}

const aiAssistantVisible = ref(false)
const { activeTab, onTabChange } = useQueryTab(['flows', 'scripts', 'tasks'] as const, 'flows')

const STATUS_LABELS: Record<string, string> = { created: "已创建", active: "生效中", disabled: "已停用", archived: "已归档" }
const statusLabel = (s: string) => STATUS_LABELS[s] ?? s
const STATUS_TAGS: Record<string, string> = { created: "info", active: "success", disabled: "warning", archived: "info" }
const statusTag = (s: string) => STATUS_TAGS[s] ?? "info"
const TYPE_LABELS: Record<string, string> = { sql: "SQL", logparse: "日志解析", python: "Python(沙箱)", script: "脚本采集(目标机)" }
const typeLabel = (s: string) => TYPE_LABELS[s] ?? s
const TYPE_TAGS: Record<string, string> = { sql: "", logparse: "warning", python: "success", script: "danger" }
const typeTag = (s: string) => TYPE_TAGS[s] ?? "info"
const LANGUAGE_LABELS: Record<string, string> = { python: "Python", bash: "Bash", powershell: "PowerShell" }
const languageLabel = (lang?: string) => LANGUAGE_LABELS[lang ?? "python"] ?? "Python"

// 数据源必填: python 采集在平台沙箱执行, 不使用数据源; 日志解析可改用数据源分类
const dsRequired = computed(() =>
  form.collectType !== "python" && (form.collectType !== "logparse" || !form.dataSourceCategoryId),
)
// 脚本采集只能在"执行目标"(ssh)上跑; 其余采集方式的数据源排除执行目标
const selectableDatasources = computed(() =>
  datasources.value.filter((d) => (form.collectType === "script" ? d.type === "ssh" : d.type !== "ssh")),
)
const collectScripts = computed(() => scripts.value.filter((s) => s.scriptType === "collect"))
const pythonCollectScripts = computed(() =>
  collectScripts.value.filter((s) => !s.language || s.language === "python"),
)

const BIZ_DATE_LABELS: Record<string, string> = { natural: "当日", prev_day: "上日", calendar: "上一交易日" }
const bizDateLabel = (m: string) => BIZ_DATE_LABELS[m] ?? "当日"

const dsName = (id: number | null | undefined) => {
  const d = datasources.value.find((x) => x.id === id)
  return d ? d.name : id ? `#${id}` : "-"
}
const catName = (id: number | null | undefined) => {
  const c = dsCategories.value.find((x) => x.id === id)
  return c ? `${c.name} (${c.systemCode})` : id ? `#${id}` : "-"
}

// 分类采集: 流程未填 glob 时, 实际模式取自各数据源 globTemplate(缺失则 pathTemplate)
const catLogSources = computed(() => {
  const catId = form.dataSourceCategoryId
  if (!catId) return [] as DsRow[]
  return datasources.value.filter((x) => x.type === "log" && x.categoryId === catId && x.status !== "disabled")
})
const catLogGlobs = computed(() => {
  const seen = new Set<string>()
  for (const ds of catLogSources.value) {
    try {
      const cfg = JSON.parse(ds.config || "{}") as { globTemplate?: string; pathTemplate?: string }
      const g = cfg.globTemplate || cfg.pathTemplate
      if (g) seen.add(g)
    } catch { /* 配置非 JSON 时忽略 */ }
  }
  return Array.from(seen).sort()
})

// 非交易时点合法性: 15:00 至次日 09:00, 15 分钟一个间隔
function isValidWindowPoint(v: string): boolean {
  const m = /^(\d{1,2}):(\d{2})$/.exec(v || "")
  if (!m) return false
  const hh = Number(m[1])
  const mm = Number(m[2])
  if (mm % 15 !== 0) return false
  return hh >= 15 || hh < 9 || (hh === 9 && mm === 0)
}

// 非交易时点选项: 当日 15:00-23:45 / 次日 00:00-09:00 (15 分钟间隔)
const buildWindowOptions = () => {
  const pad = (n: number) => String(n).padStart(2, "0")
  const fmt = (m: number) => `${pad(Math.floor(m / 60))}:${pad(m % 60)}`
  const evening: string[] = []
  for (let m = 15 * 60; m < 24 * 60; m += 15) evening.push(fmt(m))
  const morning: string[] = []
  for (let m = 0; m <= 9 * 60; m += 15) morning.push(fmt(m))
  return { evening, morning }
}
const { evening: windowOptionsEvening, morning: windowOptionsMorning } = buildWindowOptions()

const filterStatus = ref("")
const filterType = ref("")
const filterMetric = ref("")
const { items, total, loading, error, page, pageSize, reload, params } = useList<FlowRow>("/api/v1/flows")

const metrics = ref<MetRow[]>([])
const datasources = ref<DsRow[]>([])
const dsCategories = ref<DsCatRow[]>([])
const scripts = ref<ScriptRow[]>([])
const systems = ref<{ systemCode: string; name: string }[]>([])
async function loadOptions() {
  try {
    const m = (await http.get("/api/v1/metrics", { params: { page: 1, pageSize: 500 } })) as unknown as { items: MetRow[] }
    metrics.value = (m.items ?? []).filter((x) => x.status === "active")
    const d = (await http.get("/api/v1/data-sources")) as unknown as DsRow[]
    datasources.value = d ?? []
    const dc = (await http.get("/api/v1/data-source-categories")) as unknown as DsCatRow[]
    dsCategories.value = dc ?? []
    const s = (await http.get("/api/v1/scripts")) as unknown as ScriptRow[]
    scripts.value = s ?? []
    const sys = (await http.get("/api/v1/systems", { params: { page: 1, pageSize: 200 } })) as unknown as {
      items: { systemCode: string; name: string }[]
    }
    systems.value = sys.items ?? []
  } catch { /* 下拉加载失败不阻塞列表 */ }
}

// ---------- AI 生成流程配置（FR-AI-01） ----------
const aiDesc = ref("")
const aiSystemCode = ref("")
const aiGenLoading = ref(false)
const aiDraft = ref<Record<string, unknown> | null>(null)
const aiDraftRaw = ref("")
const aiNotice = ref("")
const aiActive = ref<string[]>([])

function aiStr(v: unknown): string {
  return typeof v === "string" ? v : v == null ? "" : String(v)
}

async function doAiGen() {
  if (!aiDesc.value.trim()) { ElMessage.warning("请先描述采集流程需求"); return }
  aiGenLoading.value = true
  try {
    const d = (await http.post("/api/v1/ai/flow-generate", {
      description: aiDesc.value.trim(),
      systemCode: aiSystemCode.value,
    })) as unknown as { draft: Record<string, unknown>; raw: string; notice: string; reviewNeeded: boolean }
    aiDraft.value = d.draft ?? null
    aiDraftRaw.value = aiDraft.value ? JSON.stringify(aiDraft.value, null, 2) : d.raw
    aiNotice.value = d.notice
    if (d.reviewNeeded) ElMessage.warning("AI 建议已生成，请人工审核后应用到表单")
  } finally {
    aiGenLoading.value = false
  }
}

function applyAiDraft() {
  const d = aiDraft.value
  if (!d) return
  const ct = d.collectType
  if (typeof ct === "string" && ["sql", "logparse", "python"].includes(ct)) form.collectType = ct
  const cc = (d.collectConfig ?? {}) as Record<string, unknown>
  if (form.collectType === "sql") {
    Object.assign(form.sqlConfig, {
      sqlTemplate: aiStr(cc.sqlTemplate ?? cc.sql_template),
      valueColumn: aiStr(cc.valueColumn ?? cc.value_column),
      aggregate: aiStr(cc.aggregate) || "single",
    })
  } else if (form.collectType === "logparse") {
    const mode = aiStr(cc.mode)
    if (mode === "regex" || mode === "delimited" || mode === "json") form.logConfig.mode = mode
    if (cc.pattern) form.logConfig.pattern = aiStr(cc.pattern)
    if (cc.delimiters) form.logConfig.delimiters = aiStr(cc.delimiters)
    if (cc.valueField ?? cc.value_field) form.logConfig.valueField = aiStr(cc.valueField ?? cc.value_field)
    if (cc.unitField ?? cc.unit_field) form.logConfig.unitField = aiStr(cc.unitField ?? cc.unit_field)
    if (cc.fileGlob ?? cc.file_glob) form.logConfig.fileGlob = aiStr(cc.fileGlob ?? cc.file_glob)
  } else if (form.collectType === "python") {
    const sid = Number(cc.scriptId)
    if (sid > 0) {
      form.pyConfig.scriptId = sid
      form.pyConfig.version = Number(cc.version) || 0
    }
  }
  const kc = (d.cleanConfig ?? {}) as Record<string, unknown>
  if (kc.dedupTake) form.clean.dedupTake = aiStr(kc.dedupTake)
  if (Array.isArray(kc.transforms)) {
    form.clean.transforms = (kc.transforms as Record<string, unknown>[]).map((t) => ({
      type: aiStr(t.type) || "unit",
      target: aiStr(t.target),
      a: Number(t.a) || 0,
      b: Number(t.b) || 0,
      digits: Number(t.digits) || 2,
    }))
  }
  const kScript = kc.script as { scriptId?: number } | undefined
  if (kScript?.scriptId) form.clean.scriptId = kScript.scriptId
  if (d.scheduleMode === "normal" || d.scheduleMode === "trading_day") form.scheduleMode = d.scheduleMode
  if (aiStr(d.cronExpr)) form.cronExpr = aiStr(d.cronExpr)
  if (d.bizDateMode === "natural" || d.bizDateMode === "prev_day" || d.bizDateMode === "calendar") form.bizDateMode = d.bizDateMode
  const wt = aiStr(d.windowTime)
  const wp = wt.includes("-") ? wt.split("-")[0] : wt
  if (isValidWindowPoint(wp)) form.windowTime = wp
  ElMessage.success("AI 建议已应用到表单，请审核后预览试跑再保存")
}

// ---------- 表单 ----------
const formVisible = ref(false)
const saving = ref(false)
const editingId = ref<number | null>(null)
const form = reactive({
  flowCode: "",
  name: "",
  metricCode: "",
  dataSourceId: null as number | null,
  dataSourceCategoryId: null as number | null,
  collectType: "sql",
  scheduleMode: "normal",
  cronExpr: "",
  windowTime: "09:00",
  bizDateMode: "natural",
  remark: "",
  sqlConfig: { sqlTemplate: "", valueColumn: "", aggregate: "single" },
  logConfig: { mode: "regex", pattern: "", delimiters: ",", valueField: "", unitField: "", fileGlob: "", startMarker: "", endMarker: "", linePattern: "", prefixes: "", excludePrefixes: "", output: "avg" },
  pyConfig: { scriptId: null as number | null, version: 0 },
  scriptConfig: { scriptId: null as number | null, version: 0, params: [] as { key: string; value: string }[] },
  clean: { dedupTake: "", transforms: [] as { type: string; target: string; a: number; b: number; digits: number }[], scriptId: null as number | null },
})
const fieldsText = ref("{}")

function blank() {
  Object.assign(form, {
    flowCode: "", name: "", metricCode: "", dataSourceId: null, dataSourceCategoryId: null, collectType: "sql",
    scheduleMode: "normal", cronExpr: "", windowTime: "09:00", bizDateMode: "natural", remark: "",
    sqlConfig: { sqlTemplate: "", valueColumn: "", aggregate: "single" },
    logConfig: { mode: "regex", pattern: "", delimiters: ",", valueField: "", unitField: "", fileGlob: "", startMarker: "", endMarker: "", linePattern: "", prefixes: "", excludePrefixes: "", output: "avg" },
    pyConfig: { scriptId: null, version: 0 },
    scriptConfig: { scriptId: null, version: 0, params: [] },
    clean: { dedupTake: "", transforms: [], scriptId: null },
  })
  fieldsText.value = "{}"
}
function openCreate() {
  editingId.value = null
  blank()
  formVisible.value = true
}
function openEdit(row: FlowRow) {
  editingId.value = row.id
  blank()
  const cc = safeParse(row.collectConfig)
  const kc = safeParse(row.cleanConfig)
  // 回显调度模式(修复: 此前未回显, 交易日模式编辑时误显示为普通 cron)
  // 非交易时点: 兼容存量范围 "HH:MM-HH:MM"(取起点)与非法值(回退默认 09:00)
  const wt = row.windowTime || ""
  const wp = wt.includes("-") ? wt.split("-")[0] : wt
  const windowTime = isValidWindowPoint(wp) ? wp : "09:00"
  Object.assign(form, {
    flowCode: row.flowCode, name: row.name, metricCode: row.metricCode,
    dataSourceId: row.dataSourceId ?? null, dataSourceCategoryId: row.dataSourceCategoryId ?? null, collectType: row.collectType,
    scheduleMode: row.scheduleMode || "normal",
    cronExpr: row.cronExpr || "",
    windowTime,
    bizDateMode: row.bizDateMode === "calendar" || row.bizDateMode === "prev_day" ? row.bizDateMode : "natural",
    remark: row.remark || "",
  })
  if (row.collectType === "sql") {
    Object.assign(form.sqlConfig, {
      sqlTemplate: cc.sqlTemplate || cc.sql_template || "",
      valueColumn: cc.valueColumn || cc.value_column || "",
      aggregate: cc.aggregate || "single",
    })
  } else if (row.collectType === "logparse") {
    Object.assign(form.logConfig, {
      mode: cc.mode || "regex", pattern: cc.pattern || "",
      delimiters: cc.delimiters || ",", valueField: cc.valueField || cc.value_field || "",
      unitField: cc.unitField || cc.unit_field || "", fileGlob: cc.fileGlob || cc.file_glob || "",
      startMarker: cc.startMarker || "", endMarker: cc.endMarker || "",
      linePattern: cc.linePattern || "",
      prefixes: Array.isArray(cc.prefixes) ? cc.prefixes.join(",") : (cc.prefixes || ""),
      excludePrefixes: Array.isArray(cc.excludePrefixes) ? cc.excludePrefixes.join(",") : (cc.excludePrefixes || ""),
      output: cc.output || "avg",
    })
    fieldsText.value = cc.fields ? JSON.stringify(cc.fields) : "{}"
  } else if (row.collectType === "python") {
    const scriptId = Number(cc.scriptId)
    Object.assign(form.pyConfig, {
      scriptId: Number.isFinite(scriptId) && scriptId > 0 ? scriptId : null,
      version: Number(cc.version) || 0,
    })
  } else {
    const params = (cc.params ?? {}) as Record<string, string>
    const scriptId = Number(cc.scriptId)
    Object.assign(form.scriptConfig, {
      scriptId: Number.isFinite(scriptId) && scriptId > 0 ? scriptId : null,
      version: Number(cc.version) || 0,
      params: Object.keys(params).map((k) => ({ key: k, value: String(params[k]) })),
    })
  }
  form.clean.dedupTake = (kc.dedupTake as string) || ""
  form.clean.transforms = Array.isArray(kc.transforms) ? kc.transforms.map((t: Record<string, unknown>) => ({
    type: (t.type as string) || "unit", target: (t.target as string) || "",
    a: (t.a as number) || 0, b: (t.b as number) || 0, digits: (t.digits as number) || 2,
  })) : []
  const cleanScriptId = Number((kc.script as { scriptId?: number } | undefined)?.scriptId)
  form.clean.scriptId = Number.isFinite(cleanScriptId) && cleanScriptId > 0 ? cleanScriptId : null
  formVisible.value = true
}
function safeParse(s: string): Record<string, unknown> {
  try { return JSON.parse(s) || {} } catch { return {} }
}

// 采集方式切离日志解析时清空分类(分类仅 logparse 可用); 切到 python 时清空数据源(沙箱不访问数据源)
watch(
  () => form.collectType,
  (t) => {
    if (t !== "logparse") form.dataSourceCategoryId = null
    if (t === "python") form.dataSourceId = null
  },
)

function buildBody() {
  let collectConfig: Record<string, unknown>
  if (form.collectType === "sql") {
    collectConfig = { ...form.sqlConfig }
  } else if (form.collectType === "logparse") {
    if (form.logConfig.mode === "section") {
      collectConfig = {
        mode: "section",
        startMarker: form.logConfig.startMarker,
        endMarker: form.logConfig.endMarker,
        linePattern: form.logConfig.linePattern,
        prefixes: (form.logConfig.prefixes || "").split(",").map((s: string) => s.trim()).filter(Boolean),
        excludePrefixes: (form.logConfig.excludePrefixes || "").split(",").map((s: string) => s.trim()).filter(Boolean),
        output: form.logConfig.output || "avg",
      }
      if (form.logConfig.fileGlob) collectConfig.fileGlob = form.logConfig.fileGlob
    } else {
      // 仅输出当前模式使用的字段, 避免 section 专属空字段(如 prefixes 字符串)混入导致后端解析失败
      const cc2: Record<string, unknown> = {
        mode: form.logConfig.mode,
        valueField: form.logConfig.valueField,
        unitField: form.logConfig.unitField,
      }
      if (form.logConfig.mode === "regex") cc2.pattern = form.logConfig.pattern
      if (form.logConfig.mode === "delimited") {
        cc2.delimiters = form.logConfig.delimiters
        let fields: Record<string, number> = {}
        try { fields = JSON.parse(fieldsText.value || "{}") } catch { fields = {} }
        cc2.fields = fields
      }
      if (form.logConfig.fileGlob) cc2.fileGlob = form.logConfig.fileGlob
      collectConfig = cc2
    }
  } else if (form.collectType === "python") {
    collectConfig = { ...form.pyConfig }
  } else {
    // 脚本采集: 入参名值对 → 对象, 空名忽略
    const params: Record<string, string> = {}
    for (const p of form.scriptConfig.params) {
      const k = p.key.trim()
      if (k) params[k] = p.value
    }
    collectConfig = { scriptId: form.scriptConfig.scriptId, version: form.scriptConfig.version, params }
  }
  const clean: Record<string, unknown> = {}
  if (form.clean.dedupTake) clean.dedupTake = form.clean.dedupTake
  if (form.clean.transforms.length) clean.transforms = form.clean.transforms
  if (form.clean.scriptId) clean.script = { scriptId: form.clean.scriptId }
  return {
    flowCode: form.flowCode, name: form.name, metricCode: form.metricCode,
    dataSourceId: form.dataSourceId || null,
    dataSourceCategoryId: form.dataSourceCategoryId || null,
    collectType: form.collectType,
    collectConfig,
    cleanConfig: Object.keys(clean).length ? clean : {},
    scheduleMode: form.scheduleMode ?? "normal",
    cronExpr: form.scheduleMode === "trading_day" ? undefined : form.cronExpr,
    windowTime: form.scheduleMode === "trading_day" ? form.windowTime : undefined,
    bizDateMode: form.bizDateMode,
    remark: form.remark,
  }
}
async function saveForm() {
  if (!form.flowCode.trim() || !form.name.trim()) { ElMessage.warning("请填写流程编码与名称"); return }
  if (!form.metricCode) { ElMessage.warning("请选择目标指标"); return }
  if (form.collectType === "logparse") {
    if (form.dataSourceId && form.dataSourceCategoryId) { ElMessage.warning("数据源与数据源分类只能二选一"); return }
    if (!form.dataSourceId && !form.dataSourceCategoryId) { ElMessage.warning("请选择数据源或数据源分类"); return }
  } else if (form.collectType !== "python" && !form.dataSourceId) {
    ElMessage.warning("请选择数据源")
    return
  }
  if (form.scheduleMode === "normal" && !form.cronExpr.trim()) { ElMessage.warning("请填写 cron 表达式"); return }
  if (form.scheduleMode === "trading_day" && !form.windowTime) { ElMessage.warning("请选择非交易时点"); return }
  if (form.collectType === "python" && !form.pyConfig.scriptId) { ElMessage.warning("请选择 Python 脚本"); return }
  if (form.collectType === "script" && !form.scriptConfig.scriptId) { ElMessage.warning("请选择脚本"); return }
  saving.value = true
  try {
    const body = buildBody()
    if (editingId.value) {
      const { flowCode, metricCode, collectType, scheduleMode, ...rest } = body as Record<string, unknown>
      await http.put(`/api/v1/flows/${editingId.value}`, rest)
      ElMessage.success("已更新（版本 +1，需重新预览后生效）")
    } else {
      await http.post("/api/v1/flows", body)
      ElMessage.success("已创建（created 状态，请预览试跑后生效）")
    }
    formVisible.value = false
    await reload()
  } finally {
    saving.value = false
  }
}

// ---------- 操作 ----------
const previewingId = ref<number | null>(null)
const triggeringId = ref<number | null>(null)
const previewVisible = ref(false)
const preview = ref<PreviewRes | null>(null)
const previewRaw = ref<{ raw: string }[]>([])
const detailVisible = ref(false)
const detail = ref<FlowRow | null>(null)

async function doPreview(row: FlowRow) {
  previewingId.value = row.id
  previewVisible.value = false
  try {
    const r = (await http.post(`/api/v1/flows/${row.id}/preview`)) as unknown as PreviewRes
    preview.value = r
    previewRaw.value = (r.rawPreview ?? []).map((v) => ({ raw: v }))
    previewVisible.value = true
    if (r.status !== "success") ElMessage.error("预览失败: " + (r.errorMsg || r.status))
  } finally {
    previewingId.value = null
  }
}
async function doTrigger(row: FlowRow) {
  try {
    await ElMessageBox.confirm(`确认手工触发流程「${row.flowCode}」执行一次?`, "提示", { type: "info" })
  } catch { return }
  triggeringId.value = row.id
  try {
    await http.post(`/api/v1/flows/${row.id}/trigger`)
    ElMessage.success("已触发, 请在任务监控查看结果")
  } finally {
    triggeringId.value = null
  }
}
async function doLifecycle(row: FlowRow, action: "activate" | "disable" | "archive", label: string) {
  try {
    await ElMessageBox.confirm(`确认${label}流程「${row.flowCode}」?`, "提示", { type: "warning" })
  } catch { return }
  await http.post(`/api/v1/flows/${row.id}/${action}`)
  ElMessage.success(`已${label}`)
  await reload()
}
async function doDelete(row: FlowRow) {
  try {
    await ElMessageBox.confirm(
      `确认删除已归档流程「${row.flowCode}」? 删除后不可恢复, 该流程的执行记录与清洗痕迹将一并删除。`,
      "删除流程",
      { type: "error", confirmButtonText: "删除", confirmButtonClass: "is-danger" },
    )
  } catch { return }
  try {
    await http.delete(`/api/v1/flows/${row.id}`)
    ElMessage.success("已删除")
    await reload()
  } catch { /* 后端已提示 */ }
}
function openDetail(row: FlowRow) { detail.value = row; detailVisible.value = true }
function prettyJson(s: string) { try { return JSON.stringify(JSON.parse(s), null, 2) } catch { return s || "-" } }

onMounted(() => {
  params.value = {}
  reload()
  loadOptions()
})
// 筛选条件同步到 useList 参数
watch([filterStatus, filterType, filterMetric], () => {
  params.value = {
    ...(filterStatus.value ? { status: filterStatus.value } : {}),
    ...(filterType.value ? { collectType: filterType.value } : {}),
    ...(filterMetric.value ? { metricCode: filterMetric.value } : {}),
  }
})
</script>

<style scoped>
.module-tabs { margin-bottom: 14px; }
.toolbar { display: flex; gap: 8px; align-items: center; margin-bottom: 14px; }
.pager { margin-top: 12px; display: flex; justify-content: flex-end; }
.transform-row { display: flex; gap: 8px; align-items: center; margin-bottom: 8px; }
.field-hint { font-size: 12px; color: var(--el-text-color-secondary); line-height: 1.5; }
.param-list { display: flex; flex-direction: column; gap: 6px; align-items: flex-start; }
.param-row { display: flex; gap: 8px; align-items: center; }
.op { line-height: 32px; }
.json-pre { max-height: 200px; overflow: auto; font-size: 12px; white-space: pre-wrap; word-break: break-all; }
.glob-hint { margin-top: 4px; font-size: 12px; line-height: 18px; color: var(--el-text-color-secondary); word-break: break-all; }
.glob-hint code { display: inline-block; margin-right: 4px; color: var(--el-color-primary); }
.runlog-pre { max-height: 260px; overflow: auto; margin: 0; padding: 8px 10px; font-size: 12px; line-height: 18px;
  white-space: pre-wrap; word-break: break-all; background: var(--el-fill-color-light); border-radius: 4px; }
</style>
