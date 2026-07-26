package resourceview

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/common"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func ResourceView() *schema.Resource {
	return &schema.Resource{
		Description: "Resource to manage views",

		CreateContext: resourceViewCreate,
		ReadContext:   resourceViewRead,
		DeleteContext: resourceViewDelete,
		Schema: map[string]*schema.Schema{
			"database": {
				Description: "DB Name where the view will bellow",
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
			},
			"comment": {
				Description: "View comment, it will be codified in a json along with come metadata information (like cluster name in case of clustering)",
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
			},
			"name": {
				Description: "View Name",
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
			},
			"cluster": {
				Description: "Cluster Name",
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
				Computed:    true,
			},
			"query": {
				Description: "View query",
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				StateFunc: func(val interface{}) string {
					return common.FormatSQL(val.(string))
				},
				DiffSuppressFunc: func(k, old, new string, d *schema.ResourceData) bool {
					return standardizeString(old) == standardizeString(new)
				},
				DiffSuppressOnRefresh: true,
			},
			"materialized": {
				Description: "Is materialized view",
				Type:        schema.TypeBool,
				Required:    true,
				ForceNew:    true,
			},
			"to_table": {
				Description: "For materialized view - destination table",
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
				Computed:    true,
			},
			"refresh": {
				Description: "REFRESH clause for materialized view (e.g. 'EVERY 1 HOUR')",
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
			},
			"engine": {
				Description: "Inline engine for self-contained REFRESH MV (e.g. 'MergeTree')",
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
			},
			"order_by": {
				Description: "Order by columns for inline engine",
				Type:        schema.TypeList,
				Optional:    true,
				ForceNew:    true,
				Elem: &schema.Schema{
					Type:     schema.TypeString,
					ForceNew: true,
				},
			},
			"column": {
				Description: "Column definitions for inline engine",
				Type:        schema.TypeList,
				Optional:    true,
				ForceNew:    true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"name": {
							Description: "Column Name",
							Type:        schema.TypeString,
							Required:    true,
						},
						"type": {
							Description: "Column Type",
							Type:        schema.TypeString,
							Required:    true,
						},
						"comment": {
							Description: "Column Comment",
							Type:        schema.TypeString,
							Optional:    true,
						},
						"default_kind": {
							Description: "Column Default Kind",
							Type:        schema.TypeString,
							Optional:    true,
						},
						"default_expression": {
							Description: "Column Default Expression",
							Type:        schema.TypeString,
							Optional:    true,
						},
					},
				},
			},
		},
	}
}

func standardizeString(input string) string {
	trimmed := strings.TrimSpace(input)
	lowered := strings.ToLower(trimmed)
	re := regexp.MustCompile(`\s+`)
	standardized := re.ReplaceAllString(lowered, " ")
	return standardized
}

func resourceViewRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	writer := bufio.NewWriter(os.Stdout)

	writer.Flush()

	var diags diag.Diagnostics

	client := meta.(*common.ApiClient)
	conn := client.ClickhouseConnection

	database := d.Get("database").(string)
	viewName := d.Get("name").(string)

	chViewService := CHViewService{CHConnection: conn}
	chView, err := chViewService.GetView(ctx, database, viewName)
	if chView == nil && err == nil {
		d.SetId("")
		return nil
	}

	if err != nil {
		return diag.FromErr(fmt.Errorf("reading Clickhouse view: %v", err))
	}

	viewResource, err := chView.ToResource()
	if err != nil {
		return diag.FromErr(fmt.Errorf("transforming Clickhouse view to resource: %v", err))
	}

	if err := d.Set("database", viewResource.Database); err != nil {
		return diag.FromErr(fmt.Errorf("setting database: %v", err))
	}
	if err := d.Set("name", viewResource.Name); err != nil {
		return diag.FromErr(fmt.Errorf("setting name: %v", err))
	}

	if viewResource.Cluster != "" {
		if err := d.Set("cluster", viewResource.Cluster); err != nil {
			return diag.FromErr(fmt.Errorf("setting cluster: %v", err))
		}
	}
	if err := d.Set("query", viewResource.Query); err != nil {
		return diag.FromErr(fmt.Errorf("setting cluster: %v", err))
	}
	if err := d.Set("materialized", viewResource.Materialized); err != nil {
		return diag.FromErr(fmt.Errorf("setting materialized: %v", err))
	}
	if viewResource.ToTable != "" {
		if err := d.Set("to_table", viewResource.ToTable); err != nil {
			return diag.FromErr(fmt.Errorf("setting to_table: %v", err))
		}
	}
	if viewResource.Refresh != "" {
		if err := d.Set("refresh", viewResource.Refresh); err != nil {
			return diag.FromErr(fmt.Errorf("setting refresh: %v", err))
		}
	}
	// Only fetch and set inline engine fields when the state already has engine set.
	// This avoids diffs on existing MVs created with TO table.
	if existingEngine := d.Get("engine").(string); existingEngine != "" {
		if err := d.Set("engine", existingEngine); err != nil {
			return diag.FromErr(fmt.Errorf("setting engine: %v", err))
		}
		columns, colErr := chViewService.getViewColumns(ctx, database, viewName)
		if colErr != nil {
			return diag.FromErr(fmt.Errorf("getting columns for view: %v", colErr))
		}
		if columns != nil {
			if err := d.Set("column", getColumnsForSchema(columnsToResource(columns))); err != nil {
				return diag.FromErr(fmt.Errorf("setting column: %v", err))
			}
		}
		if orderBy := d.Get("order_by"); orderBy != nil {
			if err := d.Set("order_by", orderBy); err != nil {
				return diag.FromErr(fmt.Errorf("setting order_by: %v", err))
			}
		}
	}

	d.SetId(viewResource.Cluster + ":" + database + ":" + viewName)

	return diags
}

func resourceViewCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	client := meta.(*common.ApiClient)
	conn := client.ClickhouseConnection
	viewResource := ViewResource{}
	chViewService := CHViewService{CHConnection: conn}

	viewResource.Cluster = d.Get("cluster").(string)
	viewResource.Database = d.Get("database").(string)
	viewResource.Name = d.Get("name").(string)
	viewResource.Query = d.Get("query").(string)
	viewResource.Materialized = d.Get("materialized").(bool)
	viewResource.ToTable = d.Get("to_table").(string)
	viewResource.Refresh = d.Get("refresh").(string)
	viewResource.InlineEngine = d.Get("engine").(string)
	viewResource.OrderBy = common.MapArrayInterfaceToArrayOfStrings(d.Get("order_by").([]interface{}))
	viewResource.setColumns(d.Get("column").([]interface{}))
	viewResource.Comment = common.GetComment(d.Get("comment").(string), viewResource.Cluster, &viewResource.ToTable, &viewResource.Refresh)

	if viewResource.Cluster == "" {
		viewResource.Cluster = client.DefaultCluster
	}

	diags := viewResource.Validate()
	if diags.HasError() {
		return diags
	}

	err := chViewService.CreateView(ctx, viewResource)

	if err != nil {
		return diag.FromErr(err)
	}

	d.SetId(viewResource.Cluster + ":" + viewResource.Database + ":" + viewResource.Name)

	return diags
}

func resourceViewDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	var diags diag.Diagnostics
	client := meta.(*common.ApiClient)
	conn := client.ClickhouseConnection
	chViewService := CHViewService{CHConnection: conn}

	var viewResource ViewResource
	viewResource.Database = d.Get("database").(string)
	viewResource.Name = d.Get("name").(string)
	viewResource.Cluster = d.Get("cluster").(string)
	if viewResource.Cluster == "" {
		viewResource.Cluster = client.DefaultCluster
	}

	err := chViewService.DeleteView(ctx, viewResource)

	if err != nil {
		return diag.FromErr(err)
	}
	return diags
}
