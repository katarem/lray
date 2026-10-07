package {{.Package}}.application.list;

import {{.Package}}.constants.{{.ClassName}}PanelCategoryKeys;
import {{.Package}}.constants.{{.ClassName}}PortletKeys;

import com.liferay.application.list.BasePanelApp;
import com.liferay.application.list.PanelApp;
import com.liferay.portal.kernel.model.Portlet;

import org.osgi.service.component.annotations.Component;
import org.osgi.service.component.annotations.Reference;

/**
 * @author {{.Author}}
 */
@Component(
	property = {
		"panel.app.order:Integer=100",
		"panel.category.key=" + {{.ClassName}}PanelCategoryKeys.CONTROL_PANEL_CATEGORY
	},
	service = PanelApp.class
)
public class {{.ClassName}}PanelApp extends BasePanelApp {

	@Override
	public String getPortletId() {
		return {{.ClassName}}PortletKeys.{{upper .ClassName}};
	}
{{if .Legacy}}
	@Override
	@Reference(
		target = "(javax.portlet.name=" + {{.ClassName}}PortletKeys.{{upper .ClassName}} + ")",
		unbind = "-"
	)
	public void setPortlet(Portlet portlet) {
		super.setPortlet(portlet);
	}
{{else}}
	@Override
	public Portlet getPortlet() {
		return _portlet;
	}

	@Reference(target = "(javax.portlet.name=" + {{.ClassName}}PortletKeys.{{upper .ClassName}} + ")")
	private Portlet _portlet;
{{end}}
}
