// Copyright 2021 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package core

import (
	"context"

	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/domain"
	"github.com/pingcap/tidb/util/hint"
	"github.com/pingcap/tidb/util/testleak"
)

func (s *testPlanSuite) TestLogicalOptimizeWithTraceEnabled(c *C) {
	sql := "select * from t where a in (1,2)"
	defer testleak.AfterTest(c)()
	tt := []struct {
		flags []uint64
		steps int
	}{
		{
			flags: []uint64{
				flagEliminateAgg,
				flagPushDownAgg},
			steps: 2,
		},
		{
			flags: []uint64{
				flagEliminateAgg,
				flagPushDownAgg,
				flagPrunColumns,
				flagBuildKeyInfo,
			},
			steps: 4,
		},
		{
			flags: []uint64{},
			steps: 0,
		},
	}

	for i, tc := range tt {
		comment := Commentf("case:%v sql:%s", i, sql)
		stmt, err := s.ParseOneStmt(sql, "", "")
		c.Assert(err, IsNil, comment)
		err = Preprocess(s.ctx, stmt, WithPreprocessorReturn(&PreprocessorReturn{InfoSchema: s.is}))
		c.Assert(err, IsNil, comment)
		sctx := MockContext()
		sctx.GetSessionVars().StmtCtx.EnableOptimizeTrace = true
		builder, _ := NewPlanBuilder().Init(sctx, s.is, &hint.BlockHintProcessor{})
		domain.GetDomain(sctx).MockInfoCacheAndLoadInfoSchema(s.is)
		ctx := context.TODO()
		p, err := builder.Build(ctx, stmt)
		c.Assert(err, IsNil)
		flag := uint64(0)
		for _, f := range tc.flags {
			flag = flag | f
		}
		p, err = logicalOptimize(ctx, flag, p.(LogicalPlan))
		c.Assert(err, IsNil)
		_, ok := p.(*LogicalProjection)
		c.Assert(ok, IsTrue)
		otrace := sctx.GetSessionVars().StmtCtx.LogicalOptimizeTrace
		c.Assert(otrace, NotNil)
		c.Assert(len(otrace.Steps), Equals, tc.steps)
	}
}

func (s *testPlanSuite) TestSingleRuleTraceStep(c *C) {
	defer testleak.AfterTest(c)()
	tt := []struct {
		sql             string
		flags           []uint64
		assertRuleName  string
		assertRuleSteps []assertTraceStep
	}{
		{
			sql:            "select * from t as t1 left join t as t2 on t1.a = t2.a order by t1.a limit 10;",
			flags:          []uint64{flagPrunColumns, flagBuildKeyInfo, flagPushDownTopN},
			assertRuleName: "topn_push_down",
			assertRuleSteps: []assertTraceStep{
				{
					assertAction: "Limit_6 is converted into TopN_7",
					assertReason: "",
				},
				{
					assertAction: "Sort_5 passes ByItems[test.t.a] to TopN_7",
					assertReason: "TopN_7 is Limit originally",
				},
				{
					assertAction: "TopN_8 is added and pushed into Join_3's left table",
					assertReason: "Join_3's joinType is left outer join, and all ByItems[test.t.a] contained in left table",
				},
				{
					assertAction: "TopN_8 is added as DataSource_1's parent",
					assertReason: "TopN is pushed down",
				},
				{
					assertAction: "TopN_7 is added as Join_3's parent",
					assertReason: "TopN is pushed down",
				},
			},
		},
		{
			sql:            "select * from t order by a limit 10",
			flags:          []uint64{flagPrunColumns, flagBuildKeyInfo, flagPushDownTopN},
			assertRuleName: "topn_push_down",
			assertRuleSteps: []assertTraceStep{
				{
					assertAction: "Limit_4 is converted into TopN_5",
					assertReason: "",
				},
				{
					assertAction: "Sort_3 passes ByItems[test.t.a] to TopN_5",
					assertReason: "TopN_5 is Limit originally",
				},
				{
					assertAction: "TopN_5 is added as DataSource_1's parent",
					assertReason: "TopN is pushed down",
				},
			},
		},
		{
			sql:            "select * from pt3 where ptn > 3;",
			flags:          []uint64{flagPartitionProcessor, flagPredicatePushDown, flagBuildKeyInfo, flagPrunColumns},
			assertRuleName: "partition_processor",
			assertRuleSteps: []assertTraceStep{
				{
					assertReason: "DataSource_1 has multiple needed partitions[p1,p2] after pruning",
					assertAction: "DataSource_1 becomes PartitionUnion_6 with children[TableScan_1,TableScan_1]",
				},
			},
		},
		{
			sql:            "select * from pt3 where ptn = 1;",
			flags:          []uint64{flagPartitionProcessor, flagPredicatePushDown, flagBuildKeyInfo, flagPrunColumns},
			assertRuleName: "partition_processor",
			assertRuleSteps: []assertTraceStep{
				{
					assertReason: "DataSource_1 has one needed partition[p1] after pruning",
					assertAction: "DataSource_1 becomes TableScan_1",
				},
			},
		},
		{
			sql:            "select * from pt2 where ptn in (1,2,3);",
			flags:          []uint64{flagPartitionProcessor, flagPredicatePushDown, flagBuildKeyInfo, flagPrunColumns},
			assertRuleName: "partition_processor",
			assertRuleSteps: []assertTraceStep{
				{
					assertReason: "DataSource_1 has multiple needed partitions[p1,p2] after pruning",
					assertAction: "DataSource_1 becomes PartitionUnion_7 with children[TableScan_1,TableScan_1]",
				},
			},
		},
		{
			sql:            "select * from pt2 where ptn = 1;",
			flags:          []uint64{flagPartitionProcessor, flagPredicatePushDown, flagBuildKeyInfo, flagPrunColumns},
			assertRuleName: "partition_processor",
			assertRuleSteps: []assertTraceStep{
				{
					assertReason: "DataSource_1 has one needed partition[p2] after pruning",
					assertAction: "DataSource_1 becomes TableScan_1",
				},
			},
		},
		{
			sql:            "select * from pt1 where ptn > 100;",
			flags:          []uint64{flagPartitionProcessor, flagPredicatePushDown, flagBuildKeyInfo, flagPrunColumns},
			assertRuleName: "partition_processor",
			assertRuleSteps: []assertTraceStep{
				{
					assertReason: "DataSource_1 doesn't have needed partition table after pruning",
					assertAction: "DataSource_1 becomes TableDual_5",
				},
			},
		},
		{
			sql:            "select * from pt1 where ptn in (10,20);",
			flags:          []uint64{flagPartitionProcessor, flagPredicatePushDown, flagBuildKeyInfo, flagPrunColumns},
			assertRuleName: "partition_processor",
			assertRuleSteps: []assertTraceStep{
				{
					assertReason: "DataSource_1 has multiple needed partitions[p1,p2] after pruning",
					assertAction: "DataSource_1 becomes PartitionUnion_7 with children[TableScan_1,TableScan_1]",
				},
			},
		},
		{
			sql:            "select * from pt1 where ptn < 4;",
			flags:          []uint64{flagPartitionProcessor, flagPredicatePushDown, flagBuildKeyInfo, flagPrunColumns},
			assertRuleName: "partition_processor",
			assertRuleSteps: []assertTraceStep{
				{
					assertReason: "DataSource_1 has one needed partition[p1] after pruning",
					assertAction: "DataSource_1 becomes TableScan_1",
				},
			},
		},
		{
			sql:            "select * from (t t1, t t2, t t3,t t4) union all select * from (t t5, t t6, t t7,t t8)",
			flags:          []uint64{flagBuildKeyInfo, flagPrunColumns, flagDecorrelate, flagPredicatePushDown, flagEliminateOuterJoin, flagJoinReOrder},
			assertRuleName: "join_reorder",
			assertRuleSteps: []assertTraceStep{
				{
					assertAction: "join order becomes [((t1*t2)*(t3*t4)),((t5*t6)*(t7*t8))] from original [(((t1*t2)*t3)*t4),(((t5*t6)*t7)*t8)]",
					assertReason: "join cost during reorder: [[t1, cost:10000],[t2, cost:10000],[t3, cost:10000],[t4, cost:10000],[t5, cost:10000],[t6, cost:10000],[t7, cost:10000],[t8, cost:10000]]",
				},
			},
		},
		{
			sql:            "select * from t t1, t t2, t t3 where t1.a=t2.a and t3.a=t2.a and t1.a=t3.a",
			flags:          []uint64{flagBuildKeyInfo, flagPrunColumns, flagDecorrelate, flagPredicatePushDown, flagEliminateOuterJoin, flagJoinReOrder},
			assertRuleName: "join_reorder",
			assertRuleSteps: []assertTraceStep{
				{
					assertAction: "join order becomes ((t1*t2)*t3) from original ((t1*t2)*t3)",
					assertReason: "join cost during reorder: [[((t1*t2)*t3), cost:58125],[(t1*t2), cost:32500],[(t1*t3), cost:32500],[t1, cost:10000],[t2, cost:10000],[t3, cost:10000]]",
				},
			},
		},
		{
			sql:            "select min(distinct a) from t group by a",
			flags:          []uint64{flagBuildKeyInfo, flagEliminateAgg},
			assertRuleName: "aggregation_eliminate",
			assertRuleSteps: []assertTraceStep{
				{
					assertReason: "[test.t.a] is a unique key",
					assertAction: "min(distinct ...) is simplified to min(...)",
				},
				{
					assertReason: "[test.t.a] is a unique key",
					assertAction: "Aggregation_2 is simplified to a Projection_4",
				},
			},
		},
		{
			sql:            "select 1+num from (select 1+a as num from t) t1;",
			flags:          []uint64{flagEliminateProjection},
			assertRuleName: "projection_eliminate",
			assertRuleSteps: []assertTraceStep{
				{
					assertAction: "Projection_2 is eliminated, Projection_3's expressions changed into[plus(1, plus(1, test.t.a))]",
					assertReason: "Projection_3's child Projection_2 is redundant",
				},
			},
		},
		{
			sql:            "select count(*) from t a , t b, t c",
			flags:          []uint64{flagBuildKeyInfo, flagPrunColumns, flagPushDownAgg},
			assertRuleName: "aggregation_push_down",
			assertRuleSteps: []assertTraceStep{
				{
					assertAction: "Aggregation_6 pushed down across Join_5, and Join_5 right path becomes Aggregation_8",
					assertReason: "Aggregation_6's functions[count(Column#38)] are decomposable with join",
				},
			},
		},
		{
			sql:            "select sum(c1) from (select c c1, d c2 from t a union all select a c1, b c2 from t b) x group by c2",
			flags:          []uint64{flagBuildKeyInfo, flagPrunColumns, flagPushDownAgg},
			assertRuleName: "aggregation_push_down",
			assertRuleSteps: []assertTraceStep{
				{
					assertAction: "Aggregation_8 pushed down, and Union_5's children changed into[Aggregation_11,Aggregation_12]",
					assertReason: "Aggregation_8 functions[sum(Column#28)] are decomposable with Union_5",
				},
				{
					assertAction: "Projection_6 is eliminated, and Aggregation_11's functions changed into[sum(test.t.c),firstrow(test.t.d)]",
					assertReason: "Projection_6 is directly below an Aggregation_11 and has no side effects",
				},
				{
					assertAction: "Projection_7 is eliminated, and Aggregation_12's functions changed into[sum(test.t.a),firstrow(test.t.b)]",
					assertReason: "Projection_7 is directly below an Aggregation_12 and has no side effects",
				},
			},
		},
		{
			sql:            "select max(a)-min(a) from t",
			flags:          []uint64{flagBuildKeyInfo, flagPrunColumns, flagMaxMinEliminate},
			assertRuleName: "max_min_eliminate",
			assertRuleSteps: []assertTraceStep{
				{
					assertAction: "add Sort_8,add Limit_9 during eliminating Aggregation_4 max function",
					assertReason: "Aggregation_4 has only one function[max] without group by, the columns in Aggregation_4 should be sorted",
				},
				{
					assertAction: "add Sort_10,add Limit_11 during eliminating Aggregation_6 min function",
					assertReason: "Aggregation_6 has only one function[min] without group by, the columns in Aggregation_6 should be sorted",
				},
				{
					assertAction: "Aggregation_2 splited into [Aggregation_4,Aggregation_6], and add [Join_12] to connect them during eliminating Aggregation_2 multi min/max functions",
					assertReason: "each column is sorted and can benefit from index/primary key in [Aggregation_4,Aggregation_6] and none of them has group by clause",
				},
			},
		},
		{
			sql:            "select max(e) from t",
			flags:          []uint64{flagBuildKeyInfo, flagPrunColumns, flagMaxMinEliminate},
			assertRuleName: "max_min_eliminate",
			assertRuleSteps: []assertTraceStep{
				{
					assertAction: "add Selection_4,add Sort_5,add Limit_6 during eliminating Aggregation_2 max function",
					assertReason: "Aggregation_2 has only one function[max] without group by, the columns in Aggregation_2 shouldn't be NULL and needs NULL to be filtered out, the columns in Aggregation_2 should be sorted",
				},
			},
		},
		{
			sql:            "select t1.b,t1.c from t as t1 left join t as t2 on t1.a = t2.a;",
			flags:          []uint64{flagBuildKeyInfo, flagEliminateOuterJoin},
			assertRuleName: "outer_join_eliminate",
			assertRuleSteps: []assertTraceStep{
				{
					assertAction: "Outer Join_3 is eliminated and become DataSource_1",
					assertReason: "The columns[test.t.b,test.t.c] are from outer table, and the inner join keys[test.t.a] are unique",
				},
			},
		},
		{
			sql:            "select count(distinct t1.a, t1.b) from t t1 left join t t2 on t1.b = t2.b",
			flags:          []uint64{flagPrunColumns, flagBuildKeyInfo, flagEliminateOuterJoin},
			assertRuleName: "outer_join_eliminate",
			assertRuleSteps: []assertTraceStep{
				{
					assertAction: "Outer Join_3 is eliminated and become DataSource_1",
					assertReason: "The columns[test.t.a,test.t.b] in agg are from outer table, and the agg functions are duplicate agnostic",
				},
			},
		},
	}

	for i, tc := range tt {
		sql := tc.sql
		comment := Commentf("case:%v sql:%s", i, sql)
		stmt, err := s.ParseOneStmt(sql, "", "")
		c.Assert(err, IsNil, comment)
		err = Preprocess(s.ctx, stmt, WithPreprocessorReturn(&PreprocessorReturn{InfoSchema: s.is}))
		c.Assert(err, IsNil, comment)
		sctx := MockContext()
		sctx.GetSessionVars().StmtCtx.EnableOptimizeTrace = true
		sctx.GetSessionVars().AllowAggPushDown = true
		builder, _ := NewPlanBuilder().Init(sctx, s.is, &hint.BlockHintProcessor{})
		domain.GetDomain(sctx).MockInfoCacheAndLoadInfoSchema(s.is)
		ctx := context.TODO()
		p, err := builder.Build(ctx, stmt)
		c.Assert(err, IsNil)
		flag := uint64(0)
		for _, f := range tc.flags {
			flag = flag | f
		}
		_, err = logicalOptimize(ctx, flag, p.(LogicalPlan))
		c.Assert(err, IsNil)
		otrace := sctx.GetSessionVars().StmtCtx.LogicalOptimizeTrace
		c.Assert(otrace, NotNil)
		assert := false
		for _, step := range otrace.Steps {
			if step.RuleName == tc.assertRuleName {
				assert = true
				for i, ruleStep := range step.Steps {
					c.Assert(ruleStep.Action, Equals, tc.assertRuleSteps[i].assertAction)
					c.Assert(ruleStep.Reason, Equals, tc.assertRuleSteps[i].assertReason)
				}
			}
		}
		c.Assert(assert, IsTrue)
	}
}

type assertTraceStep struct {
	assertReason string
	assertAction string
}
