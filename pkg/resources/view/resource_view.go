package resourceview

import (
	"context"
	"fmt"

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
			},
			"query": {
				Description: "View query",
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
			},
			"materialized": {
				Description: "Is materialized view",
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				ForceNew:    true,
			},
			"to_table": {
				Description: "For materialized view - destination table",
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
			},
		},
	}
}

func resourceViewRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
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

	if err := d.Set("cluster", viewResource.Cluster); err != nil {
		return diag.FromErr(fmt.Errorf("setting cluster: %v", err))
	}
	if err := d.Set("query", viewResource.Query); err != nil {
		return diag.FromErr(fmt.Errorf("setting cluster: %v", err))
	}
	if err := d.Set("materialized", viewResource.Materialized); err != nil {
		return diag.FromErr(fmt.Errorf("setting materialized: %v", err))
	}
	if err := d.Set("to_table", viewResource.ToTable); err != nil {
		return diag.FromErr(fmt.Errorf("setting to_table: %v", err))
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
	viewResource.Comment = common.GetComment(d.Get("comment").(string), viewResource.Cluster, &viewResource.ToTable)

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
